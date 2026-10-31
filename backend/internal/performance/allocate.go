// Package performance 计算并留存项目业绩、耗卡业绩、手工费和销售业绩。
package performance

import (
	"math/big"
	"slices"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/money"
)

// Slot 是项目行留存的岗位、员工和岗位比例。
type Slot struct {
	Slot         int16
	PositionID   uuid.UUID
	StaffID      *uuid.UUID
	SharePercent *int16
}

// Line 是结账时参与项目业绩、耗卡业绩和手工费计算的有效项目行。
type Line struct {
	OrderItemID     uuid.UUID
	Net             money.Amount
	Slots           []Slot
	Price           money.Amount
	LaborFeeAmount  *money.Amount
	LaborFeePercent *money.Percent
	Count           *CountConsumption
}

// CountConsumption 是扣次流水使用的次数种类及随卡留存的单次金额。
type CountConsumption struct {
	Method    string
	UnitPrice money.Amount
}

// Category 是按卡流水拆分后的支付方式金额。
type Category struct {
	Method string
	Amount money.Amount
}

type allocation struct {
	kind                string
	orderItemID         uuid.UUID
	slot                *int16
	positionID, staffID *uuid.UUID
	method              string
	paidAmount          money.Amount
	ratio               int16
	amount              money.Amount
}

var methods = []string{"cash", "unionpay", "wechat", "alipay", "debt", "red_packet", "card_principal", "card_bonus"}

func fen(a money.Amount) int64 { return a.Fen() }

func fromFen(v int64) money.Amount {
	a, err := money.FromFen(v)
	if err != nil {
		panic(err)
	}
	return a
}

// mulDiv 使用整数避免乘法溢出，并向下取整到分。
func mulDiv(a, b, n int64) int64 {
	var product, denominator big.Int
	product.Mul(big.NewInt(a), big.NewInt(b))
	denominator.SetInt64(n)
	return product.Quo(&product, &denominator).Int64()
}

func allocate(lines []Line, categories []Category, split string, ratios map[string]int) []allocation {
	lines = slices.DeleteFunc(slices.Clone(lines), func(l Line) bool { return l.Count != nil })
	amounts := make([]int64, len(methods))
	for _, c := range categories {
		i := slices.Index(methods, c.Method)
		if i < 0 {
			panic("performance: unknown category: " + c.Method)
		}
		amounts[i] += fen(c.Amount)
	}
	var total int64
	for _, l := range lines {
		total += fen(l.Net)
	}
	out := []allocation{}
	if total == 0 {
		return out
	}
	cells := make([][]int64, len(lines))
	missing := slices.Clone(amounts)
	lineMissing := make([]int64, len(lines))
	for i, l := range lines {
		cells[i] = make([]int64, len(methods))
		lineMissing[i] = fen(l.Net)
		for j, amount := range amounts {
			cells[i][j] = mulDiv(amount, fen(l.Net), total)
			lineMissing[i] -= cells[i][j]
			missing[j] -= cells[i][j]
		}
	}
	for i := range lines {
		for j := range methods {
			for lineMissing[i] > 0 && missing[j] > 0 {
				cells[i][j]++
				lineMissing[i]--
				missing[j]--
			}
		}
	}
	for i, l := range lines {
		for j, method := range methods {
			if cells[i][j] == 0 {
				continue
			}
			out = append(out, splitCell(l, method, cells[i][j], split, ratios[method])...)
		}
	}
	return out
}

func splitCell(line Line, method string, cell int64, split string, ratio int) []allocation {
	slots := slices.Clone(line.Slots)
	slices.SortFunc(slots, func(a, b Slot) int { return int(a.Slot) - int(b.Slot) })
	shares := split == "by_position"
	for _, s := range slots {
		if s.SharePercent == nil {
			shares = false
		}
	}
	filled := []Slot{}
	weights := []int64{}
	var sum int64
	for _, s := range slots {
		if s.StaffID == nil {
			continue
		}
		weight := int64(1)
		if shares {
			weight = int64(*s.SharePercent)
		}
		filled = append(filled, s)
		weights = append(weights, weight)
		sum += weight
	}
	entry := func(paid int64) allocation {
		a := fromFen(paid)
		return allocation{kind: "item", orderItemID: line.OrderItemID, method: method, paidAmount: a, ratio: int16(ratio), amount: a.PercentOf(ratio)}
	}
	if sum == 0 {
		return []allocation{entry(cell)}
	}
	paid := make([]int64, len(filled))
	remaining := cell
	for i, w := range weights {
		paid[i] = mulDiv(cell, w, sum)
		remaining -= paid[i]
	}
	paid[0] += remaining
	out := []allocation{}
	for i, s := range filled {
		if paid[i] == 0 {
			continue
		}
		a := entry(paid[i])
		a.slot, a.positionID, a.staffID = &s.Slot, &s.PositionID, s.StaffID
		out = append(out, a)
	}
	return out
}

func consumeAndLabor(lines []Line, split string, ratios map[string]int) []allocation {
	out := []allocation{}
	for _, line := range lines {
		if line.Count != nil && fen(line.Count.UnitPrice) != 0 {
			entries := splitCell(line, line.Count.Method, fen(line.Count.UnitPrice), split, ratios[line.Count.Method])
			for _, e := range entries {
				e.kind = "consume"
				out = append(out, e)
			}
		}
		fee := money.Amount{}
		if line.LaborFeeAmount != nil {
			fee = *line.LaborFeeAmount
		}
		if line.LaborFeePercent != nil {
			fee = line.Price.Share(*line.LaborFeePercent)
		}
		if fen(fee) == 0 {
			continue
		}
		for _, e := range splitCell(line, "labor", fen(fee), split, 100) {
			// 手工费只给服务员工，无人或已填岗位权重为零时不记门店记录。
			if e.staffID == nil {
				continue
			}
			e.kind = "labor"
			out = append(out, e)
		}
	}
	return out
}
