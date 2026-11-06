package dataio

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/wzhlinhai/salon/backend/internal/money"
)

var (
	customerHeaders = []string{"来源顾客ID", "姓名", "手机号(可空)", "性别(男/女/空)"}
	storedHeaders   = []string{"来源卡号", "来源顾客ID", "卡种", "本金", "赠送金", "到期日(YYYY-MM-DD,可空)", "积分(整数,可空)"}
	countHeaders    = []string{"来源卡号", "来源顾客ID", "卡种", "项目", "剩余购买次数", "剩余赠送次数", "到期日(可空)"}
	summaryHeaders  = []string{"顾客数", "储值卡数", "本金合计", "赠送金合计", "次卡套餐卡数"}
	phonePattern    = regexp.MustCompile(`^1[0-9]{10}$`)
	dateLayout      = time.DateOnly
)

// fileError 是整份文件不能预览的错误，HTTP 返回 422。
type fileError struct{ message string }

func (e *fileError) Error() string { return e.message }

func badFile(format string, args ...any) error {
	return &fileError{message: fmt.Sprintf(format, args...)}
}

type parsedFile struct {
	Summary   parsedSummary
	Customers []parsedCustomer
	Stored    []parsedStored
	Counts    []parsedCount
}

type parsedSummary struct {
	Customers         int32
	StoredCards       int32
	Principal         money.Amount
	Bonus             money.Amount
	CountPackageCards int32
}

type parsedCustomer struct {
	RowNumber int32
	SourceKey string
	Name      *string
	Phone     *string
	Gender    *string
	Err       string
}

type parsedStored struct {
	RowNumber    int32
	SourceKey    string
	CustomerKey  string
	CardTypeName string
	Principal    money.Amount
	Bonus        money.Amount
	ExpiresOn    *time.Time
	Points       int32
	Err          string
}

type parsedCount struct {
	RowNumber    int32
	SourceKey    string
	CustomerKey  string
	CardTypeName string
	ItemName     string
	Paid         int32
	Gift         int32
	ExpiresOn    *time.Time
	Err          string
}

// parseWorkbook 读取并校验中间 Excel 的工作表、表头和每行格式；行级问题写在 Err 里。
func parseWorkbook(r io.Reader) (*parsedFile, error) {
	// 解压上限必须显式设置，避免 xlsx 压缩炸弹在读取时才耗尽内存/临时空间。
	f, err := excelize.OpenReader(r, excelize.Options{UnzipSizeLimit: MaxUnzipBytes, UnzipXMLSizeLimit: MaxUnzipXMLBytes})
	if err != nil {
		return nil, badFile("无法读取 Excel 文件")
	}
	defer func() { _ = f.Close() }()

	for _, sheet := range []string{sheetCustomers, sheetStoredCards, sheetCountCards, sheetSummary} {
		if index, err := f.GetSheetIndex(sheet); err != nil || index < 0 {
			return nil, badFile("缺少工作表：%s", sheet)
		}
	}
	out := &parsedFile{}
	err = readSheet(f, sheetCustomers, MaxRowsPerSheet, customerHeaders, func(rowNumber int32, row []string) error {
		if emptyRow(row) {
			return nil
		}
		item := parsedCustomer{RowNumber: rowNumber, SourceKey: strings.TrimSpace(row[0])}
		if item.SourceKey == "" {
			item.Err = "来源顾客ID不能为空"
		}
		item.Name = optionalText(row[1])
		if phone := strings.TrimSpace(row[2]); phone != "" {
			if !phonePattern.MatchString(phone) {
				item.Err = joinError(item.Err, "手机号格式不正确")
			} else {
				item.Phone = &phone
			}
		}
		switch strings.TrimSpace(row[3]) {
		case "":
		case "男":
			item.Gender = ptr("male")
		case "女":
			item.Gender = ptr("female")
		default:
			item.Err = joinError(item.Err, "性别只能填写男、女或空")
		}
		out.Customers = append(out.Customers, item)
		return nil
	})
	if err != nil {
		return nil, err
	}

	err = readSheet(f, sheetStoredCards, MaxRowsPerSheet, storedHeaders, func(rowNumber int32, row []string) error {
		if emptyRow(row) {
			return nil
		}
		item := parsedStored{
			RowNumber:    rowNumber,
			SourceKey:    strings.TrimSpace(row[0]),
			CustomerKey:  strings.TrimSpace(row[1]),
			CardTypeName: strings.TrimSpace(row[2]),
		}
		if item.SourceKey == "" {
			item.Err = joinError(item.Err, "来源卡号不能为空")
		}
		if item.CustomerKey == "" {
			item.Err = joinError(item.Err, "来源顾客ID不能为空")
		}
		if item.CardTypeName == "" {
			item.Err = joinError(item.Err, "卡种不能为空")
		}
		if principal, err := parseMoney(row[3]); err != nil {
			item.Err = joinError(item.Err, "本金格式不正确")
		} else {
			item.Principal = principal
		}
		if bonus, err := parseMoney(row[4]); err != nil {
			item.Err = joinError(item.Err, "赠送金格式不正确")
		} else {
			item.Bonus = bonus
		}
		if expires, err := parseDate(row[5]); err != nil {
			item.Err = joinError(item.Err, "到期日格式不正确")
		} else {
			item.ExpiresOn = expires
		}
		if points, err := parseOptionalInt(row[6], 0, 99999999); err != nil {
			item.Err = joinError(item.Err, "积分格式不正确")
		} else if points != nil {
			item.Points = *points
		}
		out.Stored = append(out.Stored, item)
		return nil
	})
	if err != nil {
		return nil, err
	}

	err = readSheet(f, sheetCountCards, MaxRowsPerSheet, countHeaders, func(rowNumber int32, row []string) error {
		if emptyRow(row) {
			return nil
		}
		item := parsedCount{
			RowNumber:    rowNumber,
			SourceKey:    strings.TrimSpace(row[0]),
			CustomerKey:  strings.TrimSpace(row[1]),
			CardTypeName: strings.TrimSpace(row[2]),
			ItemName:     strings.TrimSpace(row[3]),
		}
		if item.SourceKey == "" {
			item.Err = joinError(item.Err, "来源卡号不能为空")
		}
		if item.CustomerKey == "" {
			item.Err = joinError(item.Err, "来源顾客ID不能为空")
		}
		if item.CardTypeName == "" {
			item.Err = joinError(item.Err, "卡种不能为空")
		}
		if item.ItemName == "" {
			item.Err = joinError(item.Err, "项目不能为空")
		}
		if paid, err := parseOptionalInt(row[4], 0, 9999); err != nil || paid == nil {
			item.Err = joinError(item.Err, "剩余购买次数格式不正确")
		} else {
			item.Paid = *paid
		}
		if gift, err := parseOptionalInt(row[5], 0, 9999); err != nil || gift == nil {
			item.Err = joinError(item.Err, "剩余赠送次数格式不正确")
		} else {
			item.Gift = *gift
		}
		if expires, err := parseDate(row[6]); err != nil {
			item.Err = joinError(item.Err, "到期日格式不正确")
		} else {
			item.ExpiresOn = expires
		}
		out.Counts = append(out.Counts, item)
		return nil
	})
	if err != nil {
		return nil, err
	}

	var summaries [][]string
	err = readSheet(f, sheetSummary, MaxSummaryRows, summaryHeaders, func(_ int32, row []string) error {
		if emptyRow(row) {
			return nil
		}
		summaries = append(summaries, row)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(summaries) == 0 {
		return nil, badFile("汇总工作表缺少数据")
	}
	if len(summaries) > 1 {
		return nil, badFile("汇总工作表只能有一行数据")
	}
	summary := summaries[0]
	items := []struct {
		field *int32
		value string
		max   int
	}{{&out.Summary.Customers, summary[0], 20000}, {&out.Summary.StoredCards, summary[1], 20000}, {nil, summary[2], 0}, {nil, summary[3], 0}, {&out.Summary.CountPackageCards, summary[4], 20000}}
	for _, item := range items {
		if item.field == nil {
			continue
		}
		value, err := parseOptionalInt(item.value, 0, int64(item.max))
		if err != nil || value == nil {
			return nil, badFile("汇总表条数格式不正确")
		}
		*item.field = *value
	}
	principal, err := parseMoney(summary[2])
	if err != nil {
		return nil, badFile("汇总表本金合计格式不正确")
	}
	bonus, err := parseMoney(summary[3])
	if err != nil {
		return nil, badFile("汇总表赠送金合计格式不正确")
	}
	out.Summary.Principal, out.Summary.Bonus = principal, bonus
	return out, nil
}

// readSheet 用流式 Rows 逐行读取，边读边数，超过上限立即报文件错误，不把整张表读进内存。
func readSheet(f *excelize.File, sheet string, maxRows int, headers []string, handle func(int32, []string) error) error {
	rows, err := f.Rows(sheet)
	if err != nil {
		return badFile("无法读取工作表 %s", sheet)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Error(); err != nil {
			return badFile("无法读取工作表 %s", sheet)
		}
		return badFile("工作表 %s 缺少表头", sheet)
	}
	got, err := rows.Columns()
	if err != nil {
		return badFile("无法读取工作表 %s", sheet)
	}
	if err := checkHeader(got, sheet, headers); err != nil {
		return err
	}
	dataRows := 0
	for rows.Next() {
		dataRows++
		if dataRows > maxRows {
			return badFile("工作表 %s 超过 %d 行上限", sheet, maxRows)
		}
		row, err := rows.Columns()
		if err != nil {
			return badFile("无法读取工作表 %s", sheet)
		}
		if err := handle(int32(dataRows)+1, padRow(row, len(headers))); err != nil {
			return err
		}
	}
	if err := rows.Error(); err != nil {
		return badFile("无法读取工作表 %s", sheet)
	}
	return nil
}

func checkHeader(got []string, sheet string, want []string) error {
	for i := range want {
		value := ""
		if i < len(got) {
			value = strings.TrimSpace(got[i])
		}
		if value != want[i] {
			return badFile("工作表 %s 的表头不正确，应为：%s", sheet, strings.Join(want, " | "))
		}
	}
	for i := len(want); i < len(got); i++ {
		if strings.TrimSpace(got[i]) != "" {
			return badFile("工作表 %s 的表头列数过多", sheet)
		}
	}
	return nil
}

func padRow(row []string, length int) []string {
	out := make([]string, length)
	copy(out, row)
	return out
}

func emptyRow(row []string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

func optionalText(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func ptr[T any](value T) *T { return &value }

func joinError(existing, next string) string {
	if existing == "" {
		return next
	}
	return existing + "；" + next
}

func parseMoney(value string) (money.Amount, error) {
	value = strings.TrimSpace(value)
	var out money.Amount
	if value == "" {
		return out, errors.New("empty")
	}
	if err := out.Scan(value); err != nil {
		return out, err
	}
	return out, nil
}

func parseDate(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	date, err := time.Parse(dateLayout, value)
	if err != nil {
		return nil, err
	}
	return &date, nil
}

func parseOptionalInt(value string, min, max int64) (*int32, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(value, 10, 32)
	if err != nil || n < min || n > max {
		return nil, errors.New("invalid")
	}
	return ptr(int32(n)), nil
}
