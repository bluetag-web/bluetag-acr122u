# B037 价签写卡服务 — HTTP API 文档

> 服务:`bluetag-go.exe`(零第三方依赖,Go 标准库实现)
> 监听:`http://127.0.0.1:8765`(**仅本机回环**,不对局域网/公网暴露)
> 页面:`GET /` 返回内置网页(`index.html` 需与 exe 同目录)
> 标签:Witstec B037,3.7 英寸,240×416,黑/白/红三色电子墨水屏
> 读卡器:ACR122U(PN532 直连模式)

---

## 全局约定

### CORS(跨域)

所有路由(含 `/`)都经过 CORS 中间件,供远程在线设计器跨域调用:

| 响应头 | 值 |
|---|---|
| `Access-Control-Allow-Origin` | 默认 `*`;设置环境变量 `CORS_ORIGIN` 锁定来源,如 `CORS_ORIGIN=https://designer.example.com` |
| `Access-Control-Allow-Methods` | `GET, POST, OPTIONS` |
| `Access-Control-Allow-Headers` | `Content-Type` |
| `Access-Control-Max-Age` | `86400` |

- `OPTIONS` 预检请求统一返回 `204 No Content`(带以上头)。
- 上传用 `multipart/form-data`,属于"简单请求",浏览器通常不触发预检。

### 通用错误格式

业务校验错误返回纯文本 + 对应 HTTP 状态码:

```
HTTP/1.1 400 Bad Request

multipart: request Content-Type isn't multipart/form-data
```

| 状态码 | 含义 |
|---|---|
| 200 | 成功(JSON / NDJSON;写卡中途失败也在 200 流内报错,见下) |
| 400 | 请求不合法(非 multipart、缺 `image` 字段、图片解码失败等) |
| 405 | 方法不允许 |
| 409 | 已有写卡任务进行中(仅 `/api/write`) |

### 图片处理参数(仅适用于 `/api/upload`)

均为 multipart 表单字段,全部可选:

| 字段 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `image` | file | 必填 | 图片文件(PNG / JPEG / GIF),超过 16MB 拒绝 |
| `threshold` | int | `128` | 黑白阈值:灰度 ≥ T 为白。灰度 = 0.299R + 0.587G + 0.114B |
| `redMin` | int | `120` | 红色判定:R 最小值 |
| `redDiff` | int | `40` | 红色判定:R − max(G, B) ≥ D |
| `dither` | `"true"` | `false` | Floyd–Steinberg 抖动(照片建议开);开启时忽略 `threshold`。红像素固定为白,不参与误差扩散 |
| `stretch` | `"true"` | `false` | 拉伸铺满 240×416;默认等比缩放、白底居中 |

处理管线:解码 → 缩放(240×416)→ 红色掩码 → 黑白二值(阈值/抖动)→ 打包。
注:面板行扫描 x 方向与数据相反,镜像在写卡打包层完成,**预览与统计均为原图方向**。

### `/api/write` / `/api/validate` 的输入契约(不做任何图像处理)

`/api/write` 与 `/api/validate` **均不处理图片**,只校验(后者不写卡)。缩放、红判定、二值化/抖动全部由前端完成。
前端处理管线(与内置网页 `index.html` 中的 JS 实现一致):

1. 缩放到 240×416(`stretch` 拉伸铺满,或等比缩放白底居中)
2. 红色判定:`R ≥ 120 && R − max(G, B) ≥ 40`
3. 黑白二值:灰度 ≥ threshold(默认 128),或 Floyd–Steinberg 抖动;红像素固定为白、不参与误差扩散
4. 量化为**纯三色**(白 255,255,255 / 黑 0,0,0 / 红 255,0,0)后导出 PNG 提交

服务端校验规则(任一不满足返回 400):

- 尺寸必须精确为 **240×416**
- 每个像素必须为纯白/纯黑/纯红(每通道容差 **±16**,容忍 ICC 色彩管理扰动)
- 像素必须完全不透明(alpha = 255)
- 错误信息列出前 3 个违规像素坐标,如 `(x=123,y=45)=RGB(128,64,32) 不是纯白/纯黑/纯红`

镜像(**仍在服务端打包层完成**)——前端 canvas 上的方向即最终屏幕显示方向,无需自行翻转。
旧的表单参数 `threshold`/`redMin`/`redDiff`/`dither`/`stretch` 对 `/api/write` 与 `/api/validate` 均已无效,收到即忽略。

---

## 1. GET /api/status — 读卡器状态

查询读卡器是否可用(不激活标签)。

**请求**

```bash
curl http://127.0.0.1:8765/api/status
```

**响应 200**

```json
{"busy":false,"ok":true,"reader":"ACS ACR122 0"}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `busy` | bool | `true` = 正在写卡(此调用不阻塞,可随时查询) |
| `ok` | bool | 找到 ACR 读卡器为 `true` |
| `reader` | string | 读卡器名称(自动挑选名称含 "ACR" 的第一个) |
| `error` | string | 仅 `ok:false` 时出现,PC/SC 错误信息(未插读卡器等) |

**JS 示例**

```js
const s = await (await fetch("http://127.0.0.1:8765/api/status")).json();
if (!s.ok) alert("读卡器不可用: " + s.error);
if (s.busy) alert("正在写卡,请稍候");
```

---

## 2. POST /api/upload — 图片处理 + 预览

把图片按目标参数处理成双通道位图,返回像素统计与三色预览(不触碰硬件)。
适合设计器做"实时效果预览"。

**请求**(`multipart/form-data`)

```bash
curl -X POST http://127.0.0.1:8765/api/upload \
  -F "image=@design.png" \
  -F "threshold=140" -F "dither=true"
```

**响应 200**

```json
{
  "white": 56363,
  "black": 43477,
  "red": 44468,
  "bwLen": 12480,
  "rdLen": 12480,
  "preview": "data:image/png;base64,iVBORw0KGgo..."
}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `white` / `black` | int | BW 通道像素数(白 + 黑 = 240×416 = 99840;红像素计入白) |
| `red` | int | RD 通道红色像素数(与 BW 重叠统计) |
| `bwLen` / `rdLen` | int | 通道字节数,恒为 12480 |
| `preview` | string | data URI(`data:image/png;base64,…`),240×416 PNG,白/黑/红三色模拟,**方向与用户设计一致**(镜像在写入层) |

**错误**:400,纯文本原因(非 multipart / 缺 image / 解码失败)。


## 3. POST /api/write — 写卡(NDJSON 流式进度)

校验前端**已处理好的图片**(输入契约见上文"全局约定"),然后执行完整写卡流程:
断电复位 → 激活标签 → 握手 → 写黑白通道
(52 包)→ 写红色通道(52 包)→ 触发刷新 → 保持射频场 15 秒(无源标签靠场供电刷新)。
**全程约 20 秒**,`hold` 阶段未结束前移开标签会刷新失败。

进度以 **NDJSON**(`application/x-ndjson`)流式返回:每行一个 JSON 事件,实时 flush。

**请求**(`multipart/form-data`,只有 `image` 字段有效)

```bash
curl -N -X POST http://127.0.0.1:8765/api/write -F "image=@design.png"
```

- `design.png` 必须是 **240×416 的纯三色 PNG**(白/黑/红,容差 ±16,不透明)
- 旧参数 `threshold`/`redMin`/`redDiff`/`dither`/`stretch` 已无效,收到即忽略

**校验失败**:400 + 纯文本,发生在 NDJSON 流开始之前。示例:

```
HTTP/1.1 400 Bad Request

图片校验失败: 图片尺寸必须为 240x416, 实际 512x512
```

```
HTTP/1.1 400 Bad Request

图片校验失败: 图片含非法像素: (x=123,y=45)=RGB(128,64,32) 不是纯白/纯黑/纯红; ... (共列出前 3 个)
```

**并发保护**:同一时间只允许一个写卡任务;重入返回 `409 已有写卡任务进行中`。

**响应 200 事件流**(按序)

| 事件 | 格式 | 说明 |
|---|---|---|
| 进度 | `{"type":"progress","phase":"…","done":N,"total":M}` | 见阶段表 |
| 日志 | `{"type":"log","msg":"使用读卡器: ACS ACR122 0"}` | 过程信息(含数据包重试提示) |
| 错误 | `{"type":"error","msg":"激活标签失败 (…)"}` | 流内报错,HTTP 仍为 200;此后流结束 |
| 完成 | `{"type":"done","seconds":"20.3"}` | 写卡+刷新保场全部成功 |

**phase 阶段表**

| phase | 含义 | total | done 变化 |
|---|---|---|---|
| `reset` | 读卡器断电复位 | 1 | 0 → 1 |
| `activate` | 激活标签(失败即报错) | 1 | 0 → 1 |
| `handshake` | 握手 `89 EE 00 00 01 01` | 1 | 0 → 1 |
| `BW` | 写黑白通道 | 52 | 1 → 52 |
| `RD` | 写红色通道 | 52 | 1 → 52 |
| `refresh` | 触发刷新 `89 FF 00 00 01 01` | 1 | 0 → 1 |
| `hold` | 保持射频场刷新中 | 15 | 剩余秒数倒计递减 |


**真实事件流示例**

```
{"type":"progress","phase":"reset","done":0,"total":1}
{"type":"log","msg":"断电复位 (2s)..."}
{"type":"progress","phase":"activate","done":0,"total":1}
{"type":"log","msg":"使用读卡器: ACS ACR122 0"}
{"type":"progress","phase":"activate","done":1,"total":1}
{"type":"progress","phase":"handshake","done":0,"total":1}
...
{"type":"progress","phase":"BW","done":13,"total":52}
...
{"type":"progress","phase":"hold","done":12,"total":15}
...
{"type":"done","seconds":"20.3"}
```

无标签时的错误路径示例:

```
{"type":"progress","phase":"reset","done":0,"total":1}
{"type":"log","msg":"断电复位 (2s)..."}
{"type":"progress","phase":"activate","done":0,"total":1}
{"type":"error","msg":"激活标签失败 (请将标签平放于读卡器中心偏上)"}
```

**JS 示例(fetch 流式读取,可直接用于设计器)**

```js
async function writeTag(processedBlob, onProgress, onLog) {
  // processedBlob: 前端已处理好的 240x416 纯三色 PNG Blob
  //                (缩放/红判定/二值化由前端完成, 镜像由服务端打包层处理)
  const fd = new FormData();
  fd.append("image", processedBlob, "design.png");
  const resp = await fetch("http://127.0.0.1:8765/api/write", {method: "POST", body: fd});
  if (resp.status === 409) throw new Error("已有写卡任务进行中");
  if (!resp.ok) throw new Error(await resp.text()); // 校验失败 (尺寸/非三色像素)

  const reader = resp.body.getReader();
  const dec = new TextDecoder();
  let buf = "";
  for (;;) {
    const {done, value} = await reader.read();
    if (done) break;
    buf += dec.decode(value, {stream: true});
    let i;
    while ((i = buf.indexOf("\n")) >= 0) {
      const line = buf.slice(0, i).trim();
      buf = buf.slice(i + 1);
      if (!line) continue;
      const ev = JSON.parse(line);
      if (ev.type === "progress") onProgress(ev.phase, ev.done, ev.total);
      else if (ev.type === "log") onLog(ev.msg);
      else if (ev.type === "error") throw new Error(ev.msg);
      else if (ev.type === "done") return ev.seconds;
    }
  }
}
```


**Python 示例**

```python
import json, requests

# design.png 必须是已处理好的 240x416 纯三色 PNG (服务端不处理, 只校验)
with open("design.png", "rb") as f:
    r = requests.post("http://127.0.0.1:8765/api/write",
                      files={"image": f}, stream=True, timeout=120)
r.raise_for_status()
for line in r.iter_lines(decode_unicode=True):
    if not line:
        continue
    ev = json.loads(line)
    if ev["type"] == "progress":
        print(f'\r{ev["phase"]} {ev["done"]}/{ev["total"]}', end="")
    elif ev["type"] == "error":
        raise RuntimeError(ev["msg"])
    elif ev["type"] == "done":
        print(f"\n完成, 用时 {ev['seconds']}s")
```

---

## 4. POST /api/validate — 仅校验图片(不写卡)

与 `/api/write` 使用**完全相同的校验逻辑**(240×416 纯三色,见"全局约定"的输入契约),
但校验通过后**不触碰读卡器与标签**,适合前端在正式写入前做预检。
不占用写卡锁——即使写卡任务进行中也可调用(响应中不含 `busy` 信息)。

**请求**(`multipart/form-data`,只有 `image` 字段有效)

```bash
curl -X POST http://127.0.0.1:8765/api/validate -F "image=@design.png"
```

**响应 200**(`application/json`)

```json
{
  "ok": true,
  "white": 56363,
  "black": 43477,
  "red": 44468,
  "bwLen": 12480,
  "rdLen": 12480
}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `ok` | bool | 恒为 `true`(失败时走 400) |
| `white` / `black` / `red` | int | 白 / 黑 / 红 像素数 |
| `bwLen` / `rdLen` | int | 打包后通道字节数,恒为 12480 |

**错误**:400,纯文本原因,与 `/api/write` 的校验失败信息一致
(非 multipart / 缺 `image` / 解码失败 / 尺寸不对 / 含非法像素等)。

**JS 示例**

```js
async function validateTag(processedBlob) {
  const fd = new FormData();
  fd.append("image", processedBlob, "design.png");
  const resp = await fetch("http://127.0.0.1:8765/api/validate", {method: "POST", body: fd});
  if (!resp.ok) throw new Error(await resp.text()); // 校验失败 (尺寸/非三色像素)
  return await resp.json(); // {ok, white, black, red, bwLen, rdLen}
}
```

---

## 5. GET / — 内置网页

返回 `index.html`(需与 exe 同目录,启动时读入内存)。
设计器部署后可完全替代此页面,后端只依赖上述三个 API。

---

## 6. 部署与运维备注

- **启动**:`bluetag-go.exe`(与 `index.html` 同目录);前台运行,Ctrl+C 停止
- **重启前先结束旧进程**:否则新实例因端口占用而启动失败(log.Fatal 退出)
- **限源**:上线时设置 `CORS_ORIGIN` 环境变量锁定设计器域名,开发期用默认 `*`
- **监听**:固定 `127.0.0.1:8765`,不暴露网络;API 无鉴权,靠回环隔离。
  远程 HTTPS 设计器页面可以 fetch `http://127.0.0.1:8765`(回环地址被视为可信来源,不算混合内容)
- **独占性**:写卡期间读卡器被独占;`/api/status` 的 `busy` 可用于前端禁用写卡按钮
- **图像处理在前端**:`/api/write` 只校验(240×416 纯三色)不处理;处理管线参考实现见内置网页 `index.html`
- **超时建议**:客户端对 `/api/write` 超时设 ≥ 120s(20s 写卡 + 缓冲)
- **协议细节**:见仓库根目录 `NFC_PROTOCOL.md`

