// main.go — B037 价签写卡服务入口 (Windows + ACR122U)
// 运行: bluetag-go.exe, 浏览器打开 http://localhost:8765
package main

import (
	"log"

	"bluetag-go/internal/server"
)

func main() {
	if err := server.Run(); err != nil {
		log.Fatal(err)
	}
}
