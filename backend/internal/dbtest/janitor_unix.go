//go:build unix

package dbtest

import "syscall"

// janitorSysProcAttr 让清理子进程脱离测试进程的会话，进程组信号不会连带杀掉它。
func janitorSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// janitorSupported 表示该平台支持脱离会话的清理进程。
func janitorSupported() bool {
	return true
}
