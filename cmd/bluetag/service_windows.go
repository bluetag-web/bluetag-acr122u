//go:build windows

// service_windows.go — Windows 服务支持 (golang.org/x/sys/windows/svc)
//
//	bluetag-go.exe install  注册服务 (自动启动, 失败自动重启)
//	bluetag-go.exe remove   停止并删除服务
//	bluetag-go.exe start    启动服务
//	bluetag-go.exe stop     停止服务
//
// 服务模式下日志写 %ProgramData%\bluetag-go\service.log (超过 8MB 轮转为 .old);
// 收到 Stop 后上报 STOP_PENDING 并等待写卡任务完成再退出。
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"bluetag-go/internal/server"
)

// isService: 当前进程是否被 SCM 启动
func isService() bool {
	ok, _ := svc.IsWindowsService()
	return ok
}

// runService: 以服务模式运行 (SCM 入口)
func runService() error {
	return svc.Run(serviceName, &service{})
}

// isElevated: 当前进程是否持有管理员令牌
func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// serviceCommand: 处理 install/remove/start/stop 子命令。
// 普通用户执行时自动请求 UAC 提权, 在新的管理员进程中重跑同一子命令。
func serviceCommand(args []string) error {
	cmd := args[0]
	if !isElevated() {
		return elevateSelf(args)
	}
	// 经 UAC 提权重启的子进程运行在独立控制台, 结束前等待回车以便查看输出。
	// 环境变量 BLUETAG_NOPAUSE=1 可跳过 (供自动化测试)。
	if len(args) > 1 && args[1] == elevatedMark && os.Getenv("BLUETAG_NOPAUSE") != "1" {
		defer pauseBeforeExit()
	}
	switch cmd {
	case "install":
		return installService()
	case "remove":
		return removeService()
	case "start":
		return startService()
	case "stop":
		return stopService()
	}
	return nil
}

// elevatedMark: 提权重启时附加的参数标记
const elevatedMark = "__elevated"

// elevateSelf: 以 runas 方式重启自身执行服务子命令 (触发 UAC 提权)
func elevateSelf(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	argv := strings.Join(append(args, elevatedMark), " ")
	err = windows.ShellExecute(0, windows.StringToUTF16Ptr("runas"),
		windows.StringToUTF16Ptr(exe), windows.StringToUTF16Ptr(argv),
		nil, windows.SW_SHOWNORMAL)
	if err != nil {
		return fmt.Errorf("执行 %q 需要管理员权限 (UAC 请求被取消或失败): %w",
			args[0], err)
	}
	fmt.Printf("已请求管理员权限执行 %q, 请在弹出的 UAC 窗口中确认...\n", args[0])
	return nil
}

// pauseBeforeExit: 提权子进程退出前等待回车, 避免控制台一闪而过
func pauseBeforeExit() {
	fmt.Println("\n按回车键退出...")
	bufio.NewReader(os.Stdin).ReadString('\n')
}

// service: svc.Handler 实现
type service struct{}

func (s *service) Execute(args []string, r <-chan svc.ChangeRequest,
	changes chan<- svc.Status) (bool, uint32) {

	const accepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending, WaitHint: 10000}
	setupServiceLog()
	log.Println("服务启动中...")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx, server.DefaultAddr) }()

	changes <- svc.Status{State: svc.Running, Accepts: accepted}
	log.Println("服务已启动")

loop:
	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			log.Println("收到停止请求, 等待写卡任务结束后退出...")
			// 通知 SCM: 停止中, 最长约 35s (写卡 25s + HTTP 关闭 10s)
			changes <- svc.Status{State: svc.StopPending, WaitHint: 35000}
			cancel()
			select {
			case err := <-errCh:
				if err != nil {
					log.Println("HTTP 退出:", err)
				}
			case <-time.After(40 * time.Second):
				log.Println("等待 HTTP 退出超时, 强制结束")
			}
			break loop
		}
	}
	log.Println("服务已停止")
	return false, 0
}

// installService: 注册服务 (自动启动 + 失败 5s 后自动重启)
func installService() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.Abs(exe); err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("连接服务管理器失败 (需要管理员权限): %w", err)
	}
	defer m.Disconnect()
	if s, err := m.OpenService(serviceName); err == nil {
		s.Close()
		return fmt.Errorf("服务 %s 已存在 (如需重装请先执行 %s remove)", serviceName, exe)
	}
	s, err := m.CreateService(serviceName, exe, mgr.Config{
		StartType:   mgr.StartAutomatic,
		DisplayName: "蓝签写卡服务",
		Description: "B037 电子胸卡写卡 Web 服务 (ACR122U), http://127.0.0.1:8765",
	})
	if err != nil {
		return fmt.Errorf("创建服务失败: %w", err)
	}
	defer s.Close()
	// 失败自动重启 (5s 后), 每天重置失败计数
	if err := s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
	}, 86400); err != nil {
		log.Println("设置失败恢复策略出错 (不影响服务运行):", err)
	}
	fmt.Printf("服务 %s 已注册: %s\n", serviceName, exe)
	fmt.Println("执行 \"" + filepath.Base(exe) + " start\" 启动服务")
	return nil
}

// removeService: 停止 (如在运行) 并删除服务
func removeService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("连接服务管理器失败 (需要管理员权限): %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("服务 %s 未安装", serviceName)
	}
	defer s.Close()
	if st, err := s.Query(); err == nil && st.State != svc.Stopped {
		fmt.Println("停止服务...")
		if _, err := s.Control(svc.Stop); err != nil {
			return fmt.Errorf("停止服务失败: %w", err)
		}
		if err := waitState(s, svc.Stopped, 40*time.Second); err != nil {
			return err
		}
	}
	return s.Delete()
}

func startService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("连接服务管理器失败 (需要管理员权限): %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("服务 %s 未安装 (先执行 install)", serviceName)
	}
	defer s.Close()
	if err := s.Start(); err != nil {
		return fmt.Errorf("启动服务失败: %w", err)
	}
	fmt.Printf("服务 %s 已启动, http://127.0.0.1:8765\n", serviceName)
	return nil
}

func stopService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("连接服务管理器失败 (需要管理员权限): %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("服务 %s 未安装", serviceName)
	}
	defer s.Close()
	if _, err := s.Control(svc.Stop); err != nil {
		return fmt.Errorf("停止服务失败: %w", err)
	}
	if err := waitState(s, svc.Stopped, 40*time.Second); err != nil {
		return err
	}
	fmt.Printf("服务 %s 已停止\n", serviceName)
	return nil
}

// waitState: 轮询等待服务进入目标状态
func waitState(s *mgr.Service, want svc.State, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		st, err := s.Query()
		if err != nil {
			return err
		}
		if st.State == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待服务状态超时 (当前 %d)", st.State)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// setupServiceLog: 服务模式日志写文件 (%ProgramData%\bluetag-go\service.log)
func setupServiceLog() {
	dir := filepath.Join(os.Getenv("ProgramData"), serviceName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	logPath := filepath.Join(dir, "service.log")
	// 简单大小轮转: 超过 8MB 改名为 .old
	if st, err := os.Stat(logPath); err == nil && st.Size() > 8<<20 {
		os.Rename(logPath, logPath+".old")
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(f)
	log.SetFlags(log.Ldate | log.Ltime)
}
