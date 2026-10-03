// main.go — B037 价签写卡服务入口 (Windows + ACR122U)
// 前台运行:     bluetag-go.exe [-addr 127.0.0.1:8765], 浏览器打开 http://localhost:8765
// Windows 服务: bluetag-go.exe install|remove|start|stop (需管理员权限)
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"

	"bluetag-go/internal/server"
)

// serviceName: 注册到 SCM 的服务名
const serviceName = "bluetag-go"

func main() {
	// 服务管理子命令 (Windows 实现见 service_windows.go)
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "install", "remove", "start", "stop":
			if err := serviceCommand(os.Args[1:]); err != nil {
				log.Fatal(err)
			}
			return
		}
	}
	// 被 SCM 启动 → 服务模式 (无控制台)
	if isService() {
		if err := runService(); err != nil {
			log.Fatal(err)
		}
		return
	}
	// 前台运行 (用法与旧版一致), Ctrl+C 优雅退出: 等写卡完成后关闭
	addr := flag.String("addr", server.DefaultAddr, "HTTP 监听地址")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := server.Serve(ctx, *addr); err != nil {
		log.Fatal(err)
	}
}
