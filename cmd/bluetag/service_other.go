//go:build !windows

// service_other.go — 非 Windows 平台: 不支持服务模式, 仅前台运行。
// (服务相关代码被构建标签隔离, 不参与 Linux 构建产物)
package main

import "fmt"

// isService: 非 Windows 恒为 false (服务子命令已提前拦截)
func isService() bool { return false }

// runService: 不可达 (isService 恒 false), 仅满足编译
func runService() error {
	return fmt.Errorf("服务模式仅支持 Windows")
}

// serviceCommand: 非 Windows 平台明确报错
func serviceCommand(args []string) error {
	return fmt.Errorf("子命令 %q 仅支持 Windows (Linux 请用 systemd 等)",
		args[0])
}
