# bluetag-go

Witstec B037 (3.7 寸, 240×416) 电子价签写卡 Web 服务。通过 ACR122U 读卡器
(PN532 直连 / Escape APDU) 把图片写入 B037 价签, 零第三方依赖, 编译产物为
单个 Windows 可执行文件。

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
│   └── pcsc/             # winscard.dll 直接 syscall (SCARD_SHARE_DIRECT)
├── web/                  # 前端页面 (go:embed 内嵌)
├── docs/API.md           # HTTP API 文档
├── Makefile
└── go.mod
```

依赖方向: `cmd → server → {imaging, tag, pn532, web}`, `tag → {pn532, pcsc}`,
`pn532 → pcsc`; `imaging` 仅依赖标准库。

## 环境变量

| 变量 | 默认 | 说明 |
|------|------|------|
| `CORS_ORIGIN` | `*` | 限制跨域来源, 如 `https://designer.example.com` |

## 说明

- 仅 Windows (winscard.dll), 无 cgo。
- `/api/write` 不做图像处理, 由前端完成三色化; 服务端只校验 240×416 纯三色并打包写卡。
