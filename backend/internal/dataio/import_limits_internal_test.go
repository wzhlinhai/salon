package dataio

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

type rawSheet struct {
	name string
	body string
}

func buildRawXLSX(t *testing.T, sheets []rawSheet, sharedStrings string) []byte {
	t.Helper()
	return buildRawXLSXName(t, sheets, sharedStrings, "xl/sharedStrings.xml")
}

func buildRawXLSXName(t *testing.T, sheets []rawSheet, sharedStrings, sharedStringsName string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	add := func(name, content string) {
		t.Helper()
		w, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}

	overrides := ""
	relationships := ""
	workbookSheets := ""
	for i, sheet := range sheets {
		id := i + 1
		overrides += fmt.Sprintf(`<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, id)
		relationships += fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, id, id)
		workbookSheets += fmt.Sprintf(`<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, xmlEscape(sheet.name), id, id)
		add(fmt.Sprintf("xl/worksheets/sheet%d.xml", id), worksheetXML(sheet.body))
	}
	if sharedStrings != "" {
		overrides += `<Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/>`
		relationships += `<Relationship Id="rId100" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/>`
		add(sharedStringsName, sharedStrings)
	}
	add("[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`+
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`+
		`<Default Extension="xml" ContentType="application/xml"/>`+
		`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>`+
		overrides+`</Types>`)
	add("_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>`+
		`</Relationships>`)
	add("xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`+
		`<sheets>`+workbookSheets+`</sheets></workbook>`)
	add("xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		relationships+`</Relationships>`)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func worksheetXML(body string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
		body + `</sheetData></worksheet>`
}

func xmlEscape(value string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(value))
	return buf.String()
}

func rawRow(number int, cells ...string) string {
	return `<row r="` + strconv.Itoa(number) + `">` + strings.Join(cells, "") + `</row>`
}

func inlineCell(ref, value string) string {
	return `<c r="` + ref + `" t="inlineStr"><is><t xml:space="preserve">` + xmlEscape(value) + `</t></is></c>`
}

func sharedCell(ref string, index int) string {
	return `<c r="` + ref + `" t="s"><v>` + strconv.Itoa(index) + `</v></c>`
}

func numberCell(ref, value string) string {
	return `<c r="` + ref + `"><v>` + xmlEscape(value) + `</v></c>`
}

func sharedStringsXML(values ...string) string {
	var buf strings.Builder
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	for _, value := range values {
		buf.WriteString(`<si><t xml:space="preserve">`)
		buf.WriteString(xmlEscape(value))
		buf.WriteString(`</t></si>`)
	}
	buf.WriteString(`</sst>`)
	return buf.String()
}

func validRawSheets(customerRows ...string) []rawSheet {
	customers := rawRow(1,
		inlineCell("A1", "来源顾客ID"), inlineCell("B1", "姓名"),
		inlineCell("C1", "手机号(可空)"), inlineCell("D1", "性别(男/女/空)"))
	if len(customerRows) == 0 {
		customerRows = []string{rawRow(2, inlineCell("A2", "C1"), inlineCell("B2", "顾客"), inlineCell("C2", ""), inlineCell("D2", ""))}
	}
	return []rawSheet{
		{name: "顾客", body: customers + strings.Join(customerRows, "")},
		{name: "储值卡", body: rawRow(1, inlineCell("A1", "来源卡号"), inlineCell("B1", "来源顾客ID"), inlineCell("C1", "卡种"), inlineCell("D1", "本金"), inlineCell("E1", "赠送金"), inlineCell("F1", "到期日(YYYY-MM-DD,可空)"), inlineCell("G1", "积分(整数,可空)"))},
		{name: "次卡套餐", body: rawRow(1, inlineCell("A1", "来源卡号"), inlineCell("B1", "来源顾客ID"), inlineCell("C1", "卡种"), inlineCell("D1", "项目"), inlineCell("E1", "剩余购买次数"), inlineCell("F1", "剩余赠送次数"), inlineCell("G1", "到期日(可空)"))},
		{name: "汇总", body: rawRow(1, inlineCell("A1", "顾客数"), inlineCell("B1", "储值卡数"), inlineCell("C1", "本金合计"), inlineCell("D1", "赠送金合计"), inlineCell("E1", "次卡套餐卡数")) + rawRow(2, numberCell("A2", "1"), numberCell("B2", "0"), numberCell("C2", "0"), numberCell("D2", "0"), numberCell("E2", "0"))},
	}
}

func TestBoundedUploadRead(t *testing.T) {
	_, err := parseWorkbook(strings.NewReader(strings.Repeat("x", MaxUploadBytes+1)))
	var fileErr *fileError
	if !errors.As(err, &fileErr) || fileErr.Error() != "文件超过大小上限" {
		t.Fatalf("oversize upload = %v", err)
	}
}

func TestSharedStringBombAllocation(t *testing.T) {
	const rows = 20000
	customerRows := make([]string, rows)
	for i := 1; i <= rows; i++ {
		customerRows[i-1] = rawRow(i+1, inlineCell("A"+strconv.Itoa(i+1), "C"+strconv.Itoa(i)), sharedCell("B"+strconv.Itoa(i+1), 0))
	}
	data := buildRawXLSX(t, validRawSheets(customerRows...), sharedStringsXML(strings.Repeat("x", 200000)))

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := parseWorkbook(bytes.NewReader(data))
	runtime.ReadMemStats(&after)
	var fileErr *fileError
	if !errors.As(err, &fileErr) || fileErr.Error() != "单元格内容超过 200 字" {
		t.Fatalf("bomb error = %v", err)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("shared-string bomb allocated %d bytes during parse", allocated)
	if allocated >= 64<<20 {
		t.Fatalf("bomb allocated %d bytes, want < %d", allocated, 64<<20)
	}
}

func TestAliasedSharedStringBombAllocation(t *testing.T) {
	const rows = 20000
	customerRows := make([]string, rows)
	for i := 1; i <= rows; i++ {
		customerRows[i-1] = rawRow(i+1, inlineCell("A"+strconv.Itoa(i+1), "C"+strconv.Itoa(i)), sharedCell("B"+strconv.Itoa(i+1), 0))
	}
	data := buildRawXLSXName(t, validRawSheets(customerRows...), sharedStringsXML(strings.Repeat("x", 200000)), `xl\sharedstrings.xml`)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := parseWorkbook(bytes.NewReader(data))
	runtime.ReadMemStats(&after)
	var fileErr *fileError
	if !errors.As(err, &fileErr) || fileErr.Error() != errUnsupportedStructure {
		t.Fatalf("aliased bomb error = %v", err)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("aliased shared-string bomb allocated %d bytes during parse", allocated)
	if allocated >= 64<<20 {
		t.Fatalf("aliased bomb allocated %d bytes, want < %d", allocated, 64<<20)
	}
}

func TestCheckRowRawLength(t *testing.T) {
	err := checkRow(sheetCustomers, 2, []string{strings.Repeat(" ", 201) + "X"}, customerColumns)
	var fileErr *fileError
	if !errors.As(err, &fileErr) || fileErr.Error() != "工作表 顾客 第 2 行单元格内容超过 200 字" {
		t.Fatalf("raw checkRow = %v", err)
	}
}
