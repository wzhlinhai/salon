package dataio_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/dataio"
)

func rewriteParts(t *testing.T, data []byte, replace, add map[string][]byte) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	written := map[string]bool{}
	for _, file := range reader.File {
		header := &zip.FileHeader{Name: file.Name, Method: file.Method}
		out, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		content, ok := replace[file.Name]
		if !ok {
			content, ok = add[file.Name]
		}
		if ok {
			if _, err := out.Write(content); err != nil {
				t.Fatal(err)
			}
			written[file.Name] = true
			continue
		}
		in, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(out, in); err != nil {
			t.Fatal(err)
		}
		if err := in.Close(); err != nil {
			t.Fatal(err)
		}
		written[file.Name] = true
	}
	for name, content := range add {
		if written[name] {
			continue
		}
		out, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type testZipEntry struct {
	name   string
	method uint16
	data   []byte
}

func readZipEntries(t *testing.T, data []byte) []testZipEntry {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]testZipEntry, 0, len(reader.File))
	for _, file := range reader.File {
		in, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(in)
		if err != nil {
			t.Fatal(err)
		}
		if err := in.Close(); err != nil {
			t.Fatal(err)
		}
		out = append(out, testZipEntry{name: file.Name, method: file.Method, data: content})
	}
	return out
}

func writeZipEntries(t *testing.T, entries []testZipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, entry := range entries {
		out, err := writer.CreateHeader(&zip.FileHeader{Name: entry.name, Method: entry.method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mutateZip(t *testing.T, data []byte, fn func([]testZipEntry) []testZipEntry) []byte {
	t.Helper()
	return writeZipEntries(t, fn(readZipEntries(t, data)))
}

func partData(t *testing.T, data []byte, name string) []byte {
	t.Helper()
	for _, entry := range readZipEntries(t, data) {
		if entry.name == name {
			return entry.data
		}
	}
	t.Fatalf("zip part %s not found", name)
	return nil
}

func withPartData(t *testing.T, data []byte, name string, content []byte) []byte {
	t.Helper()
	return mutateZip(t, data, func(entries []testZipEntry) []testZipEntry {
		for i := range entries {
			if entries[i].name == name {
				entries[i].data = content
				return entries
			}
		}
		return append(entries, testZipEntry{name: name, method: zip.Deflate, data: content})
	})
}

const emptySharedStrings = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"></sst>`

func withSharedStringsName(t *testing.T, data []byte, name string) []byte {
	t.Helper()
	return mutateZip(t, data, func(entries []testZipEntry) []testZipEntry {
		for i := range entries {
			if strings.EqualFold(entries[i].name, "xl/sharedstrings.xml") {
				entries[i].name = name
				return entries
			}
		}
		return append(entries, testZipEntry{name: name, method: zip.Deflate, data: []byte(emptySharedStrings)})
	})
}

func addSharedStringsAlias(t *testing.T, data []byte, name string) []byte {
	t.Helper()
	return mutateZip(t, data, func(entries []testZipEntry) []testZipEntry {
		return append(entries, testZipEntry{name: name, method: zip.Deflate, data: []byte(emptySharedStrings)})
	})
}

func (f *fixture) uploadReader(token string, body io.Reader, contentType string) upload {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, dataio.UploadPath, body)
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.Handler.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			f.t.Fatalf("decode upload response %q: %v", rec.Body.String(), err)
		}
	}
	return upload{Status: rec.Code, Body: out}
}

func multipartBody(t *testing.T, data []byte, name string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func assertFileError(t *testing.T, r upload, detail string) {
	t.Helper()
	if r.Status != http.StatusUnprocessableEntity || r.Body["detail"] != detail {
		t.Fatalf("status = %d %v, want 422 %q", r.Status, r.Body, detail)
	}
}

func partForSheet(t *testing.T, data []byte, sheet string) string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string][]byte{}
	for _, file := range reader.File {
		in, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(in)
		if err != nil {
			t.Fatal(err)
		}
		_ = in.Close()
		parts[file.Name] = content
	}
	targets := map[string]string{}
	decoder := xml.NewDecoder(bytes.NewReader(parts["xl/_rels/workbook.xml.rels"]))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "Relationship" {
			continue
		}
		var id, target string
		for _, attr := range start.Attr {
			switch attr.Name.Local {
			case "Id":
				id = attr.Value
			case "Target":
				target = attr.Value
			}
		}
		targets[id] = "xl/" + strings.TrimPrefix(target, "/")
	}
	decoder = xml.NewDecoder(bytes.NewReader(parts["xl/workbook.xml"]))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "sheet" {
			continue
		}
		var name, id string
		for _, attr := range start.Attr {
			switch attr.Name.Local {
			case "name":
				name = attr.Value
			case "id":
				id = attr.Value
			}
		}
		if name == sheet {
			return targets[id]
		}
	}
	t.Fatalf("sheet %s not found", sheet)
	return ""
}

func xmlText(value string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(value))
	return buf.String()
}

func rawCell(ref, value string) string {
	return `<c r="` + ref + `" t="inlineStr"><is><t xml:space="preserve">` + xmlText(value) + `</t></is></c>`
}

func rawValueCell(ref, value string) string {
	return `<c r="` + ref + `"><v>` + xmlText(value) + `</v></c>`
}

func rawSharedCell(ref string, index int) string {
	return `<c r="` + ref + `" t="s"><v>` + strconv.Itoa(index) + `</v></c>`
}

func rawRow(number int, cells ...string) string {
	return `<row r="` + strconv.Itoa(number) + `">` + strings.Join(cells, "") + `</row>`
}

func rawWorksheet(rows ...string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
		strings.Join(rows, "") + `</sheetData></worksheet>`)
}

func rawCustomerSheet(rows ...string) []byte {
	header := rawRow(1,
		rawCell("A1", "来源顾客ID"), rawCell("B1", "姓名"),
		rawCell("C1", "手机号(可空)"), rawCell("D1", "性别(男/女/空)"))
	return rawWorksheet(append([]string{header}, rows...)...)
}

func rawStoredSheet(rows ...string) []byte {
	header := rawRow(1,
		rawCell("A1", "来源卡号"), rawCell("B1", "来源顾客ID"), rawCell("C1", "卡种"),
		rawCell("D1", "本金"), rawCell("E1", "赠送金"), rawCell("F1", "到期日(YYYY-MM-DD,可空)"),
		rawCell("G1", "积分(整数,可空)"))
	return rawWorksheet(append([]string{header}, rows...)...)
}

func sharedStringsXML(values ...string) string {
	var buf strings.Builder
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	for _, value := range values {
		buf.WriteString(`<si><t xml:space="preserve">`)
		buf.WriteString(xmlText(value))
		buf.WriteString(`</t></si>`)
	}
	buf.WriteString(`</sst>`)
	return buf.String()
}

func TestSharedStringBombHTTP(t *testing.T) {
	f := open(t)
	const rows = 20000
	customerRows := make([]string, rows)
	for i := 1; i <= rows; i++ {
		row := i + 1
		customerRows[i-1] = rawRow(row, rawCell("A"+strconv.Itoa(row), "C"+strconv.Itoa(i)), rawSharedCell("B"+strconv.Itoa(row), 0))
	}
	data := defaultWorkbook(t)
	data = rewriteParts(t,
		data,
		map[string][]byte{partForSheet(t, data, "顾客"): rawCustomerSheet(customerRows...)},
		map[string][]byte{"xl/sharedStrings.xml": []byte(sharedStringsXML(strings.Repeat("x", 200000)))},
	)
	r := f.upload(f.Owner, data, "bomb.xlsx")
	if r.Status != http.StatusUnprocessableEntity || r.Body["detail"] != "单元格内容超过 200 字" {
		t.Fatalf("shared string bomb = %d %v", r.Status, r.Body)
	}
}

func TestSharedStringRuneLimitHTTP(t *testing.T) {
	f := open(t)
	data := defaultWorkbook(t)
	data = rewriteParts(t,
		data,
		map[string][]byte{partForSheet(t, data, "顾客"): rawCustomerSheet(
			rawRow(2, rawCell("A2", "C1"), rawSharedCell("B2", 0), rawCell("C2", ""), rawCell("D2", "")),
		)},
		map[string][]byte{"xl/sharedStrings.xml": []byte(sharedStringsXML(strings.Repeat("名", 201)))},
	)
	r := f.upload(f.Owner, data, "rune.xlsx")
	if r.Status != http.StatusUnprocessableEntity || r.Body["detail"] != "单元格内容超过 200 字" {
		t.Fatalf("shared string runes = %d %v", r.Status, r.Body)
	}
}

func TestSharedStringsByteLimitHTTP(t *testing.T) {
	f := open(t)
	entry := `<si><t>x</t></si>`
	body := strings.Repeat(entry, dataio.MaxSharedStringsBytes/len(entry)+1)
	data := defaultWorkbook(t)
	data = rewriteParts(t, data, nil, map[string][]byte{"xl/sharedStrings.xml": []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` + body + `</sst>`)})
	r := f.upload(f.Owner, data, "sst.xlsx")
	if r.Status != http.StatusUnprocessableEntity || r.Body["detail"] != "共享字符串表过大" {
		t.Fatalf("shared strings bytes = %d %v", r.Status, r.Body)
	}
}

func TestWideRowLimitHTTP(t *testing.T) {
	f := open(t)
	base := func() []byte { return defaultWorkbook(t) }
	valid := rawRow(2, rawCell("A2", "C1"), rawCell("B2", "顾客"), rawCell("C2", ""), rawCell("D2", ""))
	t.Run("column 21", func(t *testing.T) {
		data := base()
		data = rewriteParts(t, data, map[string][]byte{partForSheet(t, data, "顾客"): rawCustomerSheet(
			rawRow(2, rawCell("A2", "C1"), rawCell("B2", "顾客"), rawCell("C2", ""), rawCell("D2", ""), rawCell("U2", "x")),
		)}, nil)
		r := f.upload(f.Owner, data, "wide21.xlsx")
		if r.Status != http.StatusUnprocessableEntity || r.Body["detail"] != "工作表 顾客 第 2 行列数过多" {
			t.Fatalf("column 21 = %d %v", r.Status, r.Body)
		}
	})
	t.Run("column 16384", func(t *testing.T) {
		data := base()
		data = rewriteParts(t, data, map[string][]byte{partForSheet(t, data, "顾客"): rawCustomerSheet(
			rawRow(2, rawCell("A2", "C1"), rawCell("B2", "顾客"), rawCell("C2", ""), rawCell("D2", ""), rawCell("XFD2", "x")),
		)}, nil)
		r := f.upload(f.Owner, data, "wide16384.xlsx")
		if r.Status != http.StatusUnprocessableEntity || r.Body["detail"] != "工作表 顾客 第 2 行列数过多" {
			t.Fatalf("column 16384 = %d %v", r.Status, r.Body)
		}
	})
	t.Run("formula at column 16384", func(t *testing.T) {
		data := base()
		data = rewriteParts(t, data, map[string][]byte{partForSheet(t, data, "顾客"): rawCustomerSheet(
			rawRow(2, rawCell("A2", "C1"), rawCell("B2", "顾客"), rawCell("C2", ""), rawCell("D2", ""), `<c r="XFD2"><f>1+1</f></c>`),
		)}, nil)
		r := f.upload(f.Owner, data, "formula.xlsx")
		if r.Status != http.StatusUnprocessableEntity || r.Body["detail"] != "工作表 顾客 第 2 行列数过多" {
			t.Fatalf("formula column 16384 = %d %v", r.Status, r.Body)
		}
	})
	t.Run("empty column 21 accepted", func(t *testing.T) {
		data := base()
		data = rewriteParts(t, data, map[string][]byte{partForSheet(t, data, "顾客"): rawCustomerSheet(
			valid + `<c r="U2"/>`,
		)}, nil)
		r := f.upload(f.Owner, data, "empty21.xlsx")
		if r.Status != http.StatusOK {
			t.Fatalf("empty column 21 = %d %v", r.Status, r.Body)
		}
	})
}

func TestInlineAndValueLengthHTTP(t *testing.T) {
	f := open(t)
	t.Run("inline string", func(t *testing.T) {
		data := defaultWorkbook(t)
		data = rewriteParts(t, data, map[string][]byte{partForSheet(t, data, "顾客"): rawCustomerSheet(
			rawRow(2, rawCell("A2", "C1"), rawCell("B2", strings.Repeat("名", 201)), rawCell("C2", ""), rawCell("D2", "")),
		)}, nil)
		r := f.upload(f.Owner, data, "inline.xlsx")
		if r.Status != http.StatusUnprocessableEntity || r.Body["detail"] != "工作表 顾客 第 2 行单元格内容超过 200 字" {
			t.Fatalf("inline = %d %v", r.Status, r.Body)
		}
	})
	t.Run("cached value", func(t *testing.T) {
		data := defaultWorkbook(t)
		data = rewriteParts(t, data, map[string][]byte{partForSheet(t, data, "储值卡"): rawStoredSheet(
			rawRow(2, rawCell("A2", "K1"), rawCell("B2", "C1"), rawCell("C2", "储值卡A"), rawValueCell("D2", strings.Repeat("9", 201)), rawCell("E2", "0"), rawCell("F2", ""), rawCell("G2", "")),
		)}, nil)
		r := f.upload(f.Owner, data, "value.xlsx")
		if r.Status != http.StatusUnprocessableEntity || r.Body["detail"] != "工作表 储值卡 第 2 行单元格内容超过 200 字" {
			t.Fatalf("cached value = %d %v", r.Status, r.Body)
		}
	})
}

func TestExtraColumnBeyondHeaderHTTP(t *testing.T) {
	f := open(t)
	data := workbook(t,
		[][]string{
			{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"},
			{"C1", "顾客", "", "", "多余"},
		},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}},
		[][]string{{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}},
		[][]string{{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}, {"1", "0", "0", "0", "0"}},
	)
	r := f.upload(f.Owner, data, "extra.xlsx")
	if r.Status != http.StatusUnprocessableEntity || r.Body["detail"] != "工作表 顾客 第 2 行超出表头列数" {
		t.Fatalf("extra column = %d %v", r.Status, r.Body)
	}
}

type limitCase struct {
	sheet string
	field string
	value string
	limit int
}

func limitWorkbook(t *testing.T, item limitCase) []byte {
	t.Helper()
	customers := [][]string{
		{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"},
		{"C1", "顾客", "", ""},
	}
	stored := [][]string{
		{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"},
		{"K1", "C1", "储值卡A", "10", "0", "", ""},
	}
	counts := [][]string{
		{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"},
		{"K2", "C1", "剪发十次", "剪发", "1", "0", ""},
	}
	summary := [][]string{
		{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"},
		{"1", "0", "0", "0", "0"},
	}
	switch item.sheet {
	case "顾客":
		switch item.field {
		case "来源顾客ID":
			customers[1][0] = item.value
		case "姓名":
			customers[1][1] = item.value
		case "手机号(可空)":
			customers[1][2] = item.value
		case "性别(男/女/空)":
			customers[1][3] = item.value
		}
	case "储值卡":
		for i, header := range stored[0] {
			if header == item.field {
				stored[1][i] = item.value
			}
		}
	case "次卡套餐":
		for i, header := range counts[0] {
			if header == item.field {
				counts[1][i] = item.value
			}
		}
	case "汇总":
		for i, header := range summary[0] {
			if header == item.field {
				summary[1][i] = item.value
			}
		}
	}
	return workbook(t, customers, stored, counts, summary)
}

func TestCellLengthLimits(t *testing.T) {
	f := open(t)
	zeroAmount := "00000000000000000.00"
	zeroCount := strings.Repeat("0", 20)
	cases := []limitCase{
		{"顾客", "来源顾客ID", "", 64},
		{"顾客", "姓名", "", 40},
		{"顾客", "手机号(可空)", "", 20},
		{"顾客", "性别(男/女/空)", "", 2},
		{"储值卡", "来源卡号", "", 64},
		{"储值卡", "来源顾客ID", "", 64},
		{"储值卡", "卡种", "", 40},
		{"储值卡", "本金", "", 20},
		{"储值卡", "赠送金", "", 20},
		{"储值卡", "到期日(YYYY-MM-DD,可空)", "", 20},
		{"储值卡", "积分(整数,可空)", "", 20},
		{"次卡套餐", "来源卡号", "", 64},
		{"次卡套餐", "来源顾客ID", "", 64},
		{"次卡套餐", "卡种", "", 40},
		{"次卡套餐", "项目", "", 40},
		{"次卡套餐", "剩余购买次数", "", 20},
		{"次卡套餐", "剩余赠送次数", "", 20},
		{"次卡套餐", "到期日(可空)", "", 20},
		{"汇总", "顾客数", "", 20},
		{"汇总", "储值卡数", "", 20},
		{"汇总", "本金合计", "", 20},
		{"汇总", "赠送金合计", "", 20},
		{"汇总", "次卡套餐卡数", "", 20},
	}
	for _, item := range cases {
		item := item
		t.Run(item.sheet+"/"+item.field, func(t *testing.T) {
			over := limitWorkbook(t, limitCase{sheet: item.sheet, field: item.field, value: strings.Repeat("超", item.limit+1), limit: item.limit})
			r := f.upload(f.Owner, over, "limit.xlsx")
			want := fmt.Sprintf("工作表 %s 第 2 行「%s」超过 %d 字", item.sheet, item.field, item.limit)
			if r.Status != http.StatusUnprocessableEntity || r.Body["detail"] != want {
				t.Fatalf("over limit = %d %v, want %q", r.Status, r.Body, want)
			}

			atLimit := strings.Repeat("a", item.limit)
			switch item.field {
			case "本金", "赠送金", "本金合计", "赠送金合计":
				atLimit = zeroAmount
			case "积分(整数,可空)", "剩余购买次数", "剩余赠送次数", "顾客数", "储值卡数", "次卡套餐卡数":
				atLimit = zeroCount
			case "到期日(YYYY-MM-DD,可空)", "到期日(可空)":
				atLimit = strings.Repeat("2", item.limit)
			case "性别(男/女/空)":
				atLimit = strings.Repeat("男", item.limit)
			}
			ok := limitWorkbook(t, limitCase{sheet: item.sheet, field: item.field, value: atLimit, limit: item.limit})
			r = f.upload(f.Owner, ok, "at-limit.xlsx")
			if item.sheet == "汇总" && (item.field == "本金合计" || item.field == "赠送金合计") {
				if r.Status != http.StatusUnprocessableEntity || !strings.Contains(r.Body["detail"].(string), "格式不正确") {
					t.Fatalf("at limit rejected by length: %d %v", r.Status, r.Body)
				}
				return
			}
			if r.Status != http.StatusOK {
				t.Fatalf("at limit = %d %v", r.Status, r.Body)
			}
		})
	}
}

type blockingReader struct {
	data    []byte
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockingReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestPreviewConcurrency(t *testing.T) {
	f := open(t)
	_, managerToken := f.Hire("店长", "002", "manager", f.Role("店长"))
	firstBody, firstType := multipartBody(t, defaultWorkbook(t), "first.xlsx")
	reader := &blockingReader{data: firstBody, started: make(chan struct{}), release: make(chan struct{})}
	released := false
	release := func() {
		if !released {
			released = true
			close(reader.release)
		}
	}
	defer release()
	firstDone := make(chan upload, 1)
	go func() { firstDone <- f.uploadReader(f.Owner, reader, firstType) }()
	<-reader.started

	secondBody, secondType := multipartBody(t, defaultWorkbook(t), "second.xlsx")
	secondDone := make(chan upload, 1)
	go func() { secondDone <- f.uploadReader(f.Owner, bytes.NewReader(secondBody), secondType) }()
	select {
	case second := <-secondDone:
		if second.Status != http.StatusTooManyRequests || second.Body["detail"] != "正在处理其他导入，请稍后再试" {
			t.Fatalf("busy = %d %v", second.Status, second.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second request did not return 429 while intake slot is held")
	}

	forbiddenBody, forbiddenType := multipartBody(t, defaultWorkbook(t), "forbidden.xlsx")
	forbiddenDone := make(chan upload, 1)
	go func() { forbiddenDone <- f.uploadReader(managerToken, bytes.NewReader(forbiddenBody), forbiddenType) }()
	select {
	case forbidden := <-forbiddenDone:
		if forbidden.Status != http.StatusForbidden || forbidden.Body["detail"] != "permission denied" {
			t.Fatalf("forbidden = %d %v", forbidden.Status, forbidden.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("forbidden request did not return 403")
	}

	release()
	select {
	case first := <-firstDone:
		if first.Status != http.StatusOK {
			t.Fatalf("first preview = %d %v", first.Status, first.Body)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("first preview did not finish after release")
	}
	third := f.upload(f.Owner, defaultWorkbook(t), "third.xlsx")
	if third.Status != http.StatusOK {
		t.Fatalf("after release = %d %v", third.Status, third.Body)
	}
}

func TestCanonicalEntryNames(t *testing.T) {
	f := open(t)
	t.Run("backslash shared strings", func(t *testing.T) {
		data := withSharedStringsName(t, defaultWorkbook(t), `xl\sharedStrings.xml`)
		assertFileError(t, f.upload(f.Owner, data, "alias.xlsx"), "Excel 文件结构不受支持")
	})
	t.Run("duplicate lowercase", func(t *testing.T) {
		data := addSharedStringsAlias(t, defaultWorkbook(t), "XL/SharedStrings.XML")
		assertFileError(t, f.upload(f.Owner, data, "duplicate.xlsx"), "Excel 文件结构不受支持")
	})
	t.Run("backslash worksheet", func(t *testing.T) {
		data := defaultWorkbook(t)
		part := partForSheet(t, data, "顾客")
		data = mutateZip(t, data, func(entries []testZipEntry) []testZipEntry {
			for i := range entries {
				if entries[i].name == part {
					entries[i].name = strings.ReplaceAll(part, "/", `\`)
				}
			}
			return entries
		})
		assertFileError(t, f.upload(f.Owner, data, "sheet-alias.xlsx"), "Excel 文件结构不受支持")
	})
}

func TestSharedStringAliasNames(t *testing.T) {
	f := open(t)
	for _, name := range []string{"xl/sharedstrings.xml", "XL/SharedStrings.XML"} {
		t.Run(name, func(t *testing.T) {
			data := withSharedStringsName(t, defaultWorkbook(t), name)
			assertFileError(t, f.upload(f.Owner, data, "sst-alias.xlsx"), "Excel 文件结构不受支持")
		})
	}
}

func TestRootRelationshipTarget(t *testing.T) {
	f := open(t)
	data := defaultWorkbook(t)
	original := partData(t, data, "_rels/.rels")
	rels := bytes.Replace(original, []byte(`Target="xl/workbook.xml"`), []byte(`Target="xl/workbook2.xml"`), 1)
	if bytes.Equal(original, rels) {
		t.Fatal("root officeDocument target not found")
	}
	data = withPartData(t, data, "xl/workbook2.xml", partData(t, data, "xl/workbook.xml"))
	data = withPartData(t, data, "xl/_rels/workbook2.xml.rels", partData(t, data, "xl/_rels/workbook.xml.rels"))
	data = withPartData(t, data, "_rels/.rels", rels)
	assertFileError(t, f.upload(f.Owner, data, "root-target.xlsx"), "Excel 文件结构不受支持")
}

func TestWorksheetTargetOutside(t *testing.T) {
	f := open(t)
	t.Run("outside prefix with existing part", func(t *testing.T) {
		data := defaultWorkbook(t)
		part := partForSheet(t, data, "顾客")
		data = withPartData(t, data, "xl/evil/sheet1.xml", partData(t, data, part))
		original := partData(t, data, "xl/_rels/workbook.xml.rels")
		rels := bytes.Replace(original, []byte(`Target="`+strings.TrimPrefix(part, "xl/")+`"`), []byte(`Target="/xl/evil/sheet1.xml"`), 1)
		if bytes.Equal(original, rels) {
			t.Fatal("customer worksheet relationship not found")
		}
		data = withPartData(t, data, "xl/_rels/workbook.xml.rels", rels)
		assertFileError(t, f.upload(f.Owner, data, "outside.xlsx"), "Excel 文件结构不受支持")
	})
	t.Run("missing target", func(t *testing.T) {
		data := defaultWorkbook(t)
		part := partForSheet(t, data, "顾客")
		original := partData(t, data, "xl/_rels/workbook.xml.rels")
		rels := bytes.Replace(original, []byte(`Target="`+strings.TrimPrefix(part, "xl/")+`"`), []byte(`Target="/xl/worksheets/missing.xml"`), 1)
		if bytes.Equal(original, rels) {
			t.Fatal("customer worksheet relationship not found")
		}
		data = withPartData(t, data, "xl/_rels/workbook.xml.rels", rels)
		assertFileError(t, f.upload(f.Owner, data, "missing-target.xlsx"), "Excel 文件结构不受支持")
	})
}

func TestDuplicateBusinessSheet(t *testing.T) {
	f := open(t)
	data := defaultWorkbook(t)
	workbook := partData(t, data, "xl/workbook.xml")
	re := regexp.MustCompile(`<sheet[^>]*name="顾客"[^>]*></sheet>`)
	loc := re.FindIndex(workbook)
	if loc == nil {
		t.Fatal("customer sheet element not found")
	}
	modified := make([]byte, 0, len(workbook)+loc[1]-loc[0])
	modified = append(modified, workbook[:loc[1]]...)
	modified = append(modified, workbook[loc[0]:loc[1]]...)
	modified = append(modified, workbook[loc[1]:]...)
	data = withPartData(t, data, "xl/workbook.xml", modified)
	assertFileError(t, f.upload(f.Owner, data, "duplicate-sheet.xlsx"), "Excel 文件结构不受支持")
}

func TestAbsoluteCellRefs(t *testing.T) {
	f := open(t)
	cases := map[string]string{
		"$U$2":                  "工作表 顾客 第 2 行列数过多",
		"$XFD$2":                "工作表 顾客 第 2 行列数过多",
		"ZZZZZZZZZZZZZZZZZZZZ2": "工作表 顾客 第 2 行列数过多",
	}
	for ref, want := range cases {
		ref, want := ref, want
		t.Run(ref, func(t *testing.T) {
			data := defaultWorkbook(t)
			data = withPartData(t, data, partForSheet(t, data, "顾客"), rawCustomerSheet(
				rawRow(2, rawCell("A2", "C1"), rawCell("B2", "顾客"), rawCell("C2", ""), rawCell("D2", ""), `<c r="`+ref+`"><v>1</v></c>`),
			))
			assertFileError(t, f.upload(f.Owner, data, "absolute.xlsx"), want)
		})
	}
}

func TestCellsWithoutRef(t *testing.T) {
	f := open(t)
	cells := make([]string, 21)
	for i := range cells {
		cells[i] = `<c><v>1</v></c>`
	}
	data := defaultWorkbook(t)
	data = withPartData(t, data, partForSheet(t, data, "顾客"), rawCustomerSheet(rawRow(2, cells...)))
	assertFileError(t, f.upload(f.Owner, data, "no-ref.xlsx"), "工作表 顾客 第 2 行列数过多")
}

func TestWhitespaceCellContent(t *testing.T) {
	f := open(t)
	t.Run("whitespace cached value at column 21", func(t *testing.T) {
		data := defaultWorkbook(t)
		data = withPartData(t, data, partForSheet(t, data, "顾客"), rawCustomerSheet(
			rawRow(2, rawCell("A2", "C1"), rawCell("B2", "顾客"), rawCell("C2", ""), rawCell("D2", ""), `<c r="U2"><v> </v></c>`),
		))
		assertFileError(t, f.upload(f.Owner, data, "space.xlsx"), "工作表 顾客 第 2 行列数过多")
	})
	t.Run("padded inline string", func(t *testing.T) {
		data := defaultWorkbook(t)
		data = withPartData(t, data, partForSheet(t, data, "顾客"), rawCustomerSheet(
			rawRow(2, rawCell("A2", "C1"), rawCell("B2", strings.Repeat(" ", 201)+"X"), rawCell("C2", ""), rawCell("D2", "")),
		))
		assertFileError(t, f.upload(f.Owner, data, "padded.xlsx"), "工作表 顾客 第 2 行单元格内容超过 200 字")
	})
}

func TestExtraSheetIgnored(t *testing.T) {
	f := open(t)
	data := defaultWorkbook(t)
	extra := rawWorksheet(rawRow(1, rawCell("AD1", strings.Repeat("说", 300))))
	data = withPartData(t, data, "xl/worksheets/sheet99.xml", extra)
	workbook := bytes.Replace(partData(t, data, "xl/workbook.xml"), []byte(`</sheets>`), []byte(`<sheet name="说明" sheetId="99" r:id="rId99"/></sheets>`), 1)
	data = withPartData(t, data, "xl/workbook.xml", workbook)
	rels := bytes.Replace(partData(t, data, "xl/_rels/workbook.xml.rels"), []byte(`</Relationships>`), []byte(`<Relationship Id="rId99" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet99.xml"/></Relationships>`), 1)
	data = withPartData(t, data, "xl/_rels/workbook.xml.rels", rels)
	r := f.upload(f.Owner, data, "extra-sheet.xlsx")
	if r.Status != http.StatusOK {
		t.Fatalf("extra sheet = %d %v", r.Status, r.Body)
	}
}

func TestSSTPhoneticRunIgnored(t *testing.T) {
	f := open(t)
	data := defaultWorkbook(t)
	var sst []byte
	for _, entry := range readZipEntries(t, data) {
		if entry.name == "xl/sharedStrings.xml" {
			sst = entry.data
			break
		}
	}
	if sst == nil {
		sst = []byte(emptySharedStrings)
	}
	si := `<si><t>短</t><rPh sb="0" eb="1"><t>` + strings.Repeat("音", 300) + `</t></rPh></si>`
	sst = bytes.Replace(sst, []byte(`</sst>`), []byte(si+`</sst>`), 1)
	data = withPartData(t, data, "xl/sharedStrings.xml", sst)
	r := f.upload(f.Owner, data, "phonetic.xlsx")
	if r.Status != http.StatusOK {
		t.Fatalf("phonetic run = %d %v", r.Status, r.Body)
	}
}
