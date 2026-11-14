package dataio

// SetExportPageSizeForTest 调小导出分页大小，返回恢复函数。
func SetExportPageSizeForTest(size int32) func() {
	previous := exportPageSize
	exportPageSize = size
	return func() { exportPageSize = previous }
}

// SetExportPageObserverForTest 安装分页行数观察器，返回恢复函数。
func SetExportPageObserverForTest(observer func(name string, rows int)) func() {
	previous := exportPageObserver
	exportPageObserver = observer
	return func() { exportPageObserver = previous }
}
