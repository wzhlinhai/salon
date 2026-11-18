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

// prescanWorkbook 在 excelize 解析前流式检查共享字符串和工作表，限制单元格长度和列数。
func prescanWorkbook(data []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return badFile("无法读取 Excel 文件")
	}
	parts := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		parts[f.Name] = f
	}
	if f := parts["xl/sharedStrings.xml"]; f != nil {
		if err := scanSharedStrings(f); err != nil {
			return err
		}
	}
	sheets, err := sheetNames(parts)
	if err != nil {
		return err
	}
	for name, f := range parts {
		if !strings.HasPrefix(name, "xl/worksheets/") || !strings.HasSuffix(name, ".xml") {
			continue
		}
		sheet := sheets[name]
		if sheet == "" {
			sheet = path.Base(name)
		}
		if err := scanWorksheet(f, sheet); err != nil {
			return err
		}
	}
	return nil
}

func sheetNames(parts map[string]*zip.File) (map[string]string, error) {
	workbook, rels := parts["xl/workbook.xml"], parts["xl/_rels/workbook.xml.rels"]
	if workbook == nil || rels == nil {
		return nil, badFile("无法读取 Excel 文件")
	}
	targets, err := relationshipTargets(rels)
	if err != nil {
		return nil, err
	}
	rc, err := workbook.Open()
	if err != nil {
		return nil, badFile("无法读取 Excel 文件")
	}
	defer func() { _ = rc.Close() }()
	out := map[string]string{}
	decoder := xml.NewDecoder(rc)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, badFile("无法读取 Excel 文件")
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "sheet" {
			continue
		}
		name, id := attrValue(start.Attr, "name"), attrValue(start.Attr, "id")
		if target := targets[id]; name != "" && target != "" {
			out[target] = name
		}
	}
	return out, nil
}

func relationshipTargets(f *zip.File) (map[string]string, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, badFile("无法读取 Excel 文件")
	}
	defer func() { _ = rc.Close() }()
	out := map[string]string{}
	decoder := xml.NewDecoder(rc)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, badFile("无法读取 Excel 文件")
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "Relationship" {
			continue
		}
		id, target := attrValue(start.Attr, "Id"), attrValue(start.Attr, "Target")
		if id != "" && target != "" {
			out[id] = resolveTarget(target)
		}
	}
	return out, nil
}

func resolveTarget(target string) string {
	if strings.HasPrefix(target, "/") {
		return path.Clean(strings.TrimPrefix(target, "/"))
	}
	if strings.HasPrefix(target, "xl/") {
		return path.Clean(target)
	}
	return path.Clean(path.Join("xl", target))
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
	inSI, inText, length := false, false, 0
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
				inSI, length = true, 0
			case "t":
				if inSI {
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
			case "si":
				inSI, inText = false, false
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
		row, cellRow                 int64
		inCell                       bool
		hasValue, inValue, inIS, inT bool
		cellColumn, runes            int
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
				if n, err := strconv.ParseInt(attrValue(element.Attr, "r"), 10, 64); err == nil && n > 0 {
					row = n
				} else {
					row++
				}
			case "c":
				inCell, cellRow = true, row
				cellColumn, hasValue, inValue, inIS, inT, runes = 0, false, false, false, false, 0
				if column, ok := columnIndex(attrValue(element.Attr, "r")); ok {
					cellColumn = column
				}
			case "v":
				inValue = inCell
			case "is":
				inIS = inCell
			case "t":
				inT = inIS
			case "f":
				hasValue = inCell
			}
		case xml.CharData:
			if !inCell || (!inValue && !inT) {
				continue
			}
			if strings.TrimSpace(string(element)) != "" {
				hasValue = true
			}
			runes += utf8.RuneCount(element)
		case xml.EndElement:
			switch element.Name.Local {
			case "t":
				inT = false
			case "v":
				inValue = false
			case "is":
				inIS = false
			case "c":
				if inCell && hasValue {
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

func columnIndex(ref string) (int, bool) {
	column := 0
	for _, r := range ref {
		switch {
		case r >= 'A' && r <= 'Z':
			column = column*26 + int(r-'A') + 1
		case r >= 'a' && r <= 'z':
			column = column*26 + int(r-'a') + 1
		default:
			return column, column > 0
		}
	}
	return column, column > 0
}
