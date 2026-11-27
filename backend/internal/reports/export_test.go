package reports

// SetCardBalanceRowLimit 临时调低套餐结余报表的行数上限，返回恢复函数。
func SetCardBalanceRowLimit(n int32) (restore func()) {
	old := cardBalanceRowLimit
	cardBalanceRowLimit = n
	return func() { cardBalanceRowLimit = old }
}

// StoredValueColumn 返回 card_ledger 的 kind 归入的储值日报列。
func StoredValueColumn(kind string) (int, error) {
	column, err := storedValueColumn(kind)
	return int(column), err
}
