package dataio

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	errUnsupportedStructure = "Excel 文件结构不受支持"

	sourceRelationshipOfficeDocument       = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument"
	strictSourceRelationshipOfficeDocument = "http://purl.oclc.org/ooxml/officeDocument/relationships/officeDocument"
)

type relationship struct {
	id     string
	kind   string
	target string
}

type sheetRef struct {
	name string
	rid  string
}

// readUpload 把上传内容读入内存，最多读取 MaxUploadBytes 字节，超过即报文件错误。
func readUpload(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxUploadBytes+1))
	if err != nil {
		return nil, badFile("无法读取上传文件")
	}
	if len(data) > MaxUploadBytes {
		return nil, badFile("文件超过大小上限")
	}
	return data, nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// prescanWorkbook 在 excelize 解析前扫描规范布局中的共享字符串和四张业务表。
func prescanWorkbook(data []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return badFile("无法读取 Excel 文件")
	}
	parts, err := canonicalParts(zr)
	if err != nil {
		return err
	}
	workbook, err := canonicalWorkbook(parts)
	if err != nil {
		return err
	}
	if sst := parts["xl/sharedStrings.xml"]; sst != nil {
		if err := scanSharedStrings(sst); err != nil {
			return err
		}
	}
	sheets, err := businessSheetParts(parts, workbook)
	if err != nil {
		return err
	}
	for _, name := range []string{sheetCustomers, sheetStoredCards, sheetCountCards, sheetSummary} {
		part := sheets[name]
		if part == "" {
			continue
		}
		f := parts[part]
		if f == nil {
			continue
		}
		if err := scanWorksheet(f, name); err != nil {
			return err
		}
	}
	return nil
}

func canonicalParts(zr *zip.Reader) (map[string]*zip.File, error) {
	parts := make(map[string]*zip.File, len(zr.File))
	lower := make(map[string]string, len(zr.File))
	for _, f := range zr.File {
		if canonicalNameConflict(f.Name, lower) {
			return nil, badFile(errUnsupportedStructure)
		}
		lower[strings.ToLower(f.Name)] = f.Name
		parts[f.Name] = f
	}
	if name, ok := lower["xl/sharedstrings.xml"]; ok && name != "xl/sharedStrings.xml" {
		return nil, badFile(errUnsupportedStructure)
	}
	return parts, nil
}

func canonicalNameConflict(name string, seen map[string]string) bool {
	return strings.Contains(name, `\`) || seen[strings.ToLower(name)] != ""
}

func canonicalWorkbook(parts map[string]*zip.File) (*zip.File, error) {
	rels, ok := parts["_rels/.rels"]
	if !ok {
		return nil, badFile(errUnsupportedStructure)
	}
	relationships, err := readRelationships(rels)
	if err != nil {
		return nil, err
	}
	target := ""
	for _, rel := range relationships {
		if rel.kind == sourceRelationshipOfficeDocument || rel.kind == strictSourceRelationshipOfficeDocument {
			target = rel.target
			break
		}
	}
	if target == "" {
		return nil, badFile(errUnsupportedStructure)
	}
	target = path.Clean(strings.TrimPrefix(target, "/"))
	if target != "xl/workbook.xml" {
		return nil, badFile(errUnsupportedStructure)
	}
	workbook, ok := parts[target]
	if !ok {
		return nil, badFile(errUnsupportedStructure)
	}
	return workbook, nil
}

func readRelationships(f *zip.File) ([]relationship, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, badFile("无法读取 Excel 文件")
	}
	defer func() { _ = rc.Close() }()
	out := []relationship{}
	decoder := xml.NewDecoder(rc)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, badFile(errUnsupportedStructure)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "Relationship" {
			continue
		}
		out = append(out, relationship{
			id:     attrValue(start.Attr, "Id"),
			kind:   attrValue(start.Attr, "Type"),
			target: attrValue(start.Attr, "Target"),
		})
	}
	return out, nil
}

func readWorkbookSheets(workbook *zip.File) ([]sheetRef, error) {
	rc, err := workbook.Open()
	if err != nil {
		return nil, badFile("无法读取 Excel 文件")
	}
	defer func() { _ = rc.Close() }()
	out := []sheetRef{}
	decoder := xml.NewDecoder(rc)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, badFile(errUnsupportedStructure)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "sheet" {
			continue
		}
		name := attrValue(start.Attr, "name")
		if name != "" {
			out = append(out, sheetRef{name: name, rid: attrValue(start.Attr, "id")})
		}
	}
	return out, nil
}

func businessSheetParts(parts map[string]*zip.File, workbook *zip.File) (map[string]string, error) {
	refs, err := readWorkbookSheets(workbook)
	if err != nil {
		return nil, err
	}
	business := []string{sheetCustomers, sheetStoredCards, sheetCountCards, sheetSummary}
	matches := map[string][]sheetRef{}
	any := false
	for _, ref := range refs {
		for _, name := range business {
			if strings.EqualFold(ref.name, name) {
				matches[name] = append(matches[name], ref)
				any = true
			}
		}
	}
	for _, name := range business {
		if len(matches[name]) > 1 {
			return nil, badFile(errUnsupportedStructure)
		}
	}
	out := map[string]string{}
	if !any {
		return out, nil
	}
	relsFile, ok := parts["xl/_rels/workbook.xml.rels"]
	if !ok {
		return nil, badFile(errUnsupportedStructure)
	}
	rels, err := readRelationships(relsFile)
	if err != nil {
		return nil, err
	}
	targets := map[string]string{}
	for _, rel := range rels {
		targets[rel.id] = rel.target
	}
	for _, name := range business {
		found := matches[name]
		if len(found) == 0 {
			continue
		}
		target, ok := targets[found[0].rid]
		if !ok || found[0].rid == "" {
			return nil, badFile(errUnsupportedStructure)
		}
		part := resolveWorksheetPath(target)
		if !validWorksheetPart(part, parts) {
			return nil, badFile(errUnsupportedStructure)
		}
		out[name] = part
	}
	return out, nil
}

func validWorksheetPart(part string, parts map[string]*zip.File) bool {
	if !strings.HasPrefix(part, "xl/worksheets/") {
		return false
	}
	_, ok := parts[part]
	return ok
}

func resolveWorksheetPath(target string) string {
	if strings.HasPrefix(target, "/") {
		return strings.TrimPrefix(strings.ReplaceAll(path.Clean(target), `\`, "/"), "/")
	}
	return strings.ReplaceAll(path.Clean(path.Join("xl", target)), `\`, "/")
}

func attrValue(attrs []xml.Attr, name string) string {
	for _, attr := range attrs {
		if attr.Name.Local == name {
			return attr.Value
		}
	}
	return ""
}

func scanSharedStrings(f *zip.File) error {
	rc, err := f.Open()
	if err != nil {
		return badFile("无法读取 Excel 文件")
	}
	defer func() { _ = rc.Close() }()
	counter := &countingReader{r: rc}
	decoder := xml.NewDecoder(io.LimitReader(counter, MaxSharedStringsBytes+1))
	inSI, inText := false, false
	phonetic, length := 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			if err := checkSharedStringsBytes(counter.n); err != nil {
				return err
			}
			return badFile("无法读取 Excel 文件")
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "si":
				inSI, inText, phonetic, length = true, false, 0, 0
			case "rPh":
				if inSI {
					phonetic++
				}
			case "t":
				if inSI && phonetic == 0 {
					inText = true
				}
			}
		case xml.CharData:
			if inText {
				length += utf8.RuneCount(element)
				if length > MaxCellRunes {
					return badFile("单元格内容超过 %d 字", MaxCellRunes)
				}
			}
		case xml.EndElement:
			switch element.Name.Local {
			case "t":
				inText = false
			case "rPh":
				if phonetic > 0 {
					phonetic--
				}
			case "si":
				inSI, inText, phonetic = false, false, 0
			}
		}
	}
	return checkSharedStringsBytes(counter.n)
}

func checkSharedStringsBytes(n int64) error {
	if n > MaxSharedStringsBytes {
		return badFile("共享字符串表过大")
	}
	return nil
}

func scanWorksheet(f *zip.File, sheet string) error {
	rc, err := f.Open()
	if err != nil {
		return badFile("无法读取 Excel 文件")
	}
	defer func() { _ = rc.Close() }()
	decoder := xml.NewDecoder(rc)
	var (
		row, cellRow                      int64
		previousColumn, cellColumn        int
		inCell                            bool
		hasContent, inValue, inIS, inText bool
		phonetic, runes                   int
	)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return badFile("无法读取 Excel 文件")
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "row":
				previousColumn = 0
				if n, err := strconv.ParseInt(attrValue(element.Attr, "r"), 10, 64); err == nil && n > 0 {
					row = n
				} else {
					row++
				}
			case "c":
				inCell, cellRow = true, row
				hasContent, inValue, inIS, inText, phonetic, runes = false, false, false, false, 0, 0
				if ref := attrValue(element.Attr, "r"); ref != "" {
					column, ok := parseCellColumn(ref)
					if !ok {
						return badFile("工作表 %s 第 %d 行单元格位置无效", sheet, cellRow)
					}
					cellColumn = column
				} else {
					cellColumn = previousColumn + 1
				}
				previousColumn = cellColumn
			case "v":
				inValue = inCell
			case "is":
				inIS = inCell
			case "rPh":
				if inIS {
					phonetic++
				}
			case "t":
				if inIS && phonetic == 0 {
					inText = true
				}
			case "f":
				if inCell {
					hasContent = true
				}
			}
		case xml.CharData:
			if inValue || inText {
				hasContent = true
				runes += utf8.RuneCount(element)
			}
		case xml.EndElement:
			switch element.Name.Local {
			case "t":
				inText = false
			case "rPh":
				if phonetic > 0 {
					phonetic--
				}
			case "v":
				inValue = false
			case "is":
				inIS, inText, phonetic = false, false, 0
			case "c":
				if inCell && hasContent {
					if cellColumn > MaxColumns {
						return badFile("工作表 %s 第 %d 行列数过多", sheet, cellRow)
					}
					if runes > MaxCellRunes {
						return badFile("工作表 %s 第 %d 行单元格内容超过 %d 字", sheet, cellRow, MaxCellRunes)
					}
				}
				inCell = false
			}
		}
	}
	return nil
}

func parseCellColumn(ref string) (int, bool) {
	if ref == "" {
		return 0, false
	}
	isAlpha := func(b byte) bool {
		return b == '$' || ('A' <= b && b <= 'Z') || ('a' <= b && b <= 'z')
	}
	if !isAlpha(ref[0]) {
		return 0, false
	}
	last := -1
	for i := 0; i < len(ref); i++ {
		if isAlpha(ref[i]) {
			last = i
		}
	}
	if last < 0 || last >= len(ref)-1 {
		return 0, false
	}
	colPart := strings.ReplaceAll(ref[:last+1], "$", "")
	rowPart := ref[last+1:]
	row, err := strconv.Atoi(rowPart)
	if err != nil || row <= 0 {
		return 0, false
	}
	column := 0
	for i := 0; i < len(colPart); i++ {
		c := colPart[i]
		var value int
		switch {
		case 'A' <= c && c <= 'Z':
			value = int(c-'A') + 1
		case 'a' <= c && c <= 'z':
			value = int(c-'a') + 1
		default:
			return 0, false
		}
		column = column*26 + value
		if column > MaxColumns {
			return MaxColumns + 1, true
		}
	}
	if column == 0 {
		return 0, false
	}
	return column, true
}
