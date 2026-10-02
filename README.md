# bluetag-go

蓝签3.70寸电子工牌 （NFC版）写卡 Web 服务。通过 ACR122U 读卡器(PN532 直连 / Escape APDU) 把图片写入 B037 工牌, 零第三方依赖, 编译产物为单个 Windows 可执行文件。
几年前买了这个电子工牌，那时候用华为的Mate 20刷屏显，后来换了Mate 40 Pro，NFC带不起来了，就一直闲置了，最近突然又翻出来了，就使用AI逆向了蓝签的APP，获取到了通信协议，并通过ACR122U成功写入并刷新了屏显。
支持平台:

| 平台 | PC/SC 后端 | 编译 |
|------|-----------|------|
| Windows | winscard.dll 直接 syscall | `make build` (无 cgo) |
| Linux | libpcsclite (cgo) | `make build-linux`, 需 `gcc pkg-config libpcsclite-dev` |
## 快速开始

```sh
make build      # 编译到 bin/bluetag-go.exe
make test       # 运行单元测试
make run        # 开发模式运行
```

运行后浏览器打开 <http://localhost:8765>。

## 目录结构 (golang-standards/project-layout)

```
bluetag-acr122/
├── cmd/bluetag/          # 程序入口 (main.go)
├── internal/
│   ├── server/           # HTTP 路由、CORS、API handler
│   ├── imaging/          # 图片处理: 缩放/红判定/FS 抖动/三色校验/打包
│   ├── tag/              # B037 写卡协议 (89 EE/BB/CC/FF 命令序列)
│   ├── pn532/            # PN532 透传 (InDataExchange / InCommunicateThru)
│   └── pcsc/             # PC/SC 直连层: pcsc_windows.go (winscard syscall) /
│                         #   pcsc_linux.go (libpcsclite cgo), 导出 API 一致
├── web/                  # 前端页面 (go:embed 内嵌)
├── docs/API.md           # HTTP API 文档
├── Makefile
└── go.mod
```

依赖方向: `cmd → server → {imaging, tag, pn532, web}`, `tag → {pn532, pcsc}`,`pn532 → pcsc`; `imaging` 仅依赖标准库。

## 环境变量

| 变量 | 默认 | 说明 |
|------|------|------|
| `CORS_ORIGIN` | `*` | 限制跨域来源, 如 `https://designer.example.com` |

## 说明

- `/api/write` 不做图像处理, 由前端完成三色化; 服务端只校验 240×416 纯三色并打包写卡。
- Linux 平台使用libpcsclite，暂未进行验证
- Linux 运行前提: `pcscd` 服务已启动且识别到读卡器 (`systemctl status pcscd`,  需安装 `pcscd`/`libccid` 包); Escape 命令经 `SCardControl` 发送,  ACR122U + libccid 默认支持。
- 通信协议AI分析结果在 NFC_PROTOCOL.md 中