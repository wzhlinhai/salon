//go:build !unix

package dbtest

import "syscall"

// janitorSysProcAttr 在非 Unix 平台上不创建新会话。
func janitorSysProcAttr() *syscall.SysProcAttr {
	return nil
}

// janitorSupported 表示该平台不启动清理进程。
func janitorSupported() bool {
	return false
}
