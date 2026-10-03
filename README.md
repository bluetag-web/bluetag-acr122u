# bluetag-go

蓝签3.70寸电子工牌 （NFC版）写卡 Web 服务。通过 ACR122U 读卡器(PN532 直连 / Escape APDU) 把图片写入 B037 工牌, 唯一第三方依赖为 golang.org/x/sys (仅 Windows 服务支持), 编译产物为单个可执行文件。
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

## 作为 Windows 服务运行

exe 内置服务管理子命令 (需管理员权限的终端):

```sh
bluetag-go.exe install   # 注册服务: 自动启动, 异常退出 5s 后自动重启
bluetag-go.exe start     # 启动服务
bluetag-go.exe stop      # 停止服务 (等待进行中的写卡完成再退出)
bluetag-go.exe remove    # 停止并删除服务
```

或 `make install / remove / start / stop`。说明:

- 普通用户执行子命令会**自动弹出 UAC 提权窗口**, 确认后在管理员进程中执行;
  提权子进程运行在独立控制台, 结束前等待回车以便查看输出 (设置环境变量
  `BLUETAG_NOPAUSE=1` 可跳过, 供自动化使用)。
- 双击/命令行直接运行仍为前台模式, 用法不变; `-addr` 可改监听地址。
- 服务模式下日志写 `%ProgramData%\bluetag-go\service.log` (超过 8MB 轮转为 `.old`)。
- 服务以 LocalSystem 运行, 访问 PC/SC 无权限问题; 仅监听 `127.0.0.1`, 无需配置防火墙。
- 上述子命令在 Linux 上明确报错 (Linux 请用 systemd unit 包裹前台进程)。

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