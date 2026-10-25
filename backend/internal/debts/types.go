// Package debts 管理顾客欠款、历史欠款和逐笔还款。
package debts

import (
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/money"
)

// Debt 是一笔欠款及其从早到晚的还款。
type Debt struct {
	Status       string       `json:"status" enum:"outstanding,repaid,voided"`
	ID           uuid.UUID    `json:"id"`
	Kind         string       `json:"kind" enum:"order,historical"`
	OrderID      *uuid.UUID   `json:"order_id" nullable:"true"`
	BusinessDate string       `json:"business_date" format:"date"`
	Amount       money.Amount `json:"amount"`
	Repaid       money.Amount `json:"repaid"`
	Outstanding  money.Amount `json:"outstanding"`
	Note         *string      `json:"note" nullable:"true"`
	RecordedBy   uuid.UUID    `json:"recorded_by"`
	At           time.Time    `json:"at"`
	Repayments   []Repayment  `json:"repayments"`
}

// Repayment 是针对一笔欠款的还款。
type Repayment struct {
	ID           uuid.UUID    `json:"id"`
	Amount       money.Amount `json:"amount"`
	Method       string       `json:"method" enum:"cash,unionpay,wechat,alipay"`
	BusinessDate string       `json:"business_date" format:"date"`
	RepaidBy     uuid.UUID    `json:"repaid_by"`
	At           time.Time    `json:"at"`
}

// Totals 是可超过单笔金额上限的欠款对账合计。
type Totals struct {
	Amount      string `json:"amount"`
	Repaid      string `json:"repaid"`
	Outstanding string `json:"outstanding"`
}

// CustomerDebtList 是顾客欠款及所列欠款的合计。
type CustomerDebtList struct {
	Items  []Debt `json:"items"`
	Totals Totals `json:"totals"`
}

// ListCustomerDebtsInput 是顾客欠款的筛选参数。
type ListCustomerDebtsInput struct {
	CustomerID      uuid.UUID `json:"customer_id"`
	OutstandingOnly bool      `json:"outstanding_only,omitempty"`
}

// RepayDebtInput 是指定欠款的还款金额与支付方式。
type RepayDebtInput struct {
	DebtID uuid.UUID    `json:"debt_id"`
	Amount money.Amount `json:"amount"`
	Method string       `json:"method" enum:"cash,unionpay,wechat,alipay"`
}

// RegisterHistoricalDebtInput 是迁移前欠款的登记参数。
type RegisterHistoricalDebtInput struct {
	CustomerID uuid.UUID    `json:"customer_id"`
	Amount     money.Amount `json:"amount"`
	IncurredOn string       `json:"incurred_on" format:"date"`
	Note       *string      `json:"note,omitempty" maxLength:"200"`
}

// DebtSummaryInput 是本店欠款对账的参数。
type DebtSummaryInput struct{}

// Summary 是本店欠款合计及按来源拆分的合计。
type Summary struct {
	Totals
	Order            Totals `json:"order"`
	Historical       Totals `json:"historical"`
	OutstandingCount int64  `json:"outstanding_count"`
}
