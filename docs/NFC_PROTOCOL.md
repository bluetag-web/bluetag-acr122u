# BlueTag (Witstec) NFC 写入协议分析报告

分析对象: `bluetag637308 (1).apk` — com.witstec.sz.nfcpaperanys v1.5.0
分析方法: 前期用自制 DEX 解析器 + Dalvik 反汇编器纯 Python 反汇编；
后引入 **jadx 1.5.6**（`D:\DevKit\jadx-1.5.6`，JDK17 运行）产出 `jadx_out\`，
与 smali 反汇编交叉验证。**B037 协议已在真实标签上写入成功验证。**
核心代码位置: `com.witstec.sz.nfcpaperanys.ui.activity.upd.MoreImageUpdateActivity`（writeTag/writeKey/checkVoltage/redVoltage/sendDataMde）、`com.witstec.sz.nfcpaperanys.ui.activity.VoltageTestActivity`、`com.witstec.sz.nfcpaperanys.manage.BitmapBleBmpManagement`

> **重要结论: App 按 `DrawingMainActivity.sizeType` 分流两套完全不同的协议。**
> 4.2 寸（B042, 400×300）与 3.7 寸（B037, 240×416）的命令序列、帧格式、包装方式
> 全部不同，分析/复刻时必须先确认目标标签型号。

---

## 1. 传输层：不是 NDEF，是自定义协议

应用**完全不使用 NDEF**。标签读写使用 Android NFC 原始通道：

| 技术类 | 使用位置 | 场景 |
|---|---|---|
| `android.nfc.tech.IsoDep` | MoreImageUpdateActivity (`tntag` 字段) | 电子价签图像刷新（主写流程） |
| `android.nfc.tech.NfcA` | VoltageTestActivity、writeKey() | 电压测试、密钥修改（ISO14443-3A 裸帧） |

- 连接方式: `IsoDep.get(tag)` / `NfcA.get(tag)` → `connect()` → `setTimeout(200ms)`
- 发送/接收: `IsoDep.transceive(byteArray)`（封装在 `sendDataMde()`）或 `NfcA.transceive()`
- 前台分发: `NfcAdapter.enableForegroundDispatch` + `TAG_DISCOVERED` intent filter
- 即: 标签是**模拟 ISO 14443-A 卡的电子墨水屏控制器**（支持 ISO-DEP / Type 4 风格 APDU，
  同时保留 Type 2/NTAG 风格的裸 NfcA 命令接口）

## 2. IsoDep 图像写入协议（writeTag，主流程）

### 2.1 B037（3.7 英寸，240×416）— 已真机验证 ✅

jadx 反编译 `writeTag` B037 分支（MoreImageUpdateActivity L1495-1670）+ 真机写入验证。
**所有帧都是裸透传，无 AA 包装、无序号字节**（单参数 `BitmapBleBmpManagement.setConsumption(byte[])`
对输入原样返回；带序号的 `setConsumption(int,int,byte[])` 是 BLE 用的另一套）。

完整序列（实现见 `raw_clear.py`，已刷出半白/半黑测试图）：

| 步骤 | 帧 | 响应 | 说明 |
|---|---|---|---|
| 1. 握手 | `89 EE 00 00 01 01` | 90 00 | **末字节 01**（B042 是 00） |
| 2. BW 预备 | `89 BB 00 00 01 10` | 90 00 | 只有 BB 10，无 BB 4E / CC 2B 等配置串 |
| 3. BW 数据 | 52 × `89 CC 00 00 F0 + 240B`（245B） | 90 00 | 240×52=12480B = 240×416÷8，行优先、MSB 在前，**位=1 → 白像素**。**注意 x 方向与显示相反：数据第 0 像素显示在最右，打包写入时需水平镜像**（真机验证；预览图保持原图方向，镜像仅在写卡层做） |
| 4. 红预备 | `89 BB 00 00 01 13` | 90 00 | **BB 13**（不是 B042 的 BB 26） |
| 5. 红数据 | 52 × 同上格式 | 90 00 | **位=1 → 红像素**（无红则全 0） |
| 6. 刷新 | `89 FF 00 00 01 01` | 90 00 | **末字节 01**；无源标签需保持射频场供电直到刷新完成 |

要点：
- **没有尾包**。反编译中 91B 尾包分支（`89 CC 00 00 50` + 偏移 12400 起 80B）是
  **死代码**：`for (i7 = 0; i7 <= 51; i7++)` 且 `if (i7 < 52)` 恒真，从不执行。
  实测向标签发送该尾包会被拒绝（标签回 `02 00 00`，非 9000）。
- 数据帧 245B 经 IsoDep 由 Android 自动处理；PC/SC 直连时用 InCommunicateThru
  单 I-block（前缀 PCB `0x02`）直发即可。
- BW 通道硬阈值参考：像素白 → 1；红通道：R>128 且 G≈0 判红（对应 `getRedByte075`）。

### 2.2 B042（4.2 英寸，400×300）— 早期反汇编结论，未真机验证

> 以下为此前由 smali 反汇编还原的 B042 分支结论，**我们的标签是 3.7 寸，未经真机验证**，
> 仅供对照参考。

- 单通道 15000 字节 = 75 包 × 200 字节，数据帧 `89 CC 0000 C8 <200B>`（Lc=0xC8）
- 命令序列含 `BB 4E`、`CC 4F`、`CC 2B`、`CC 26`、`BB 24` 等配置串，握手末字节 00，
  刷新末字节 00
- 同样存在死代码尾包分支（长度前缀 0x50）

### 2.3 帧格式与响应

- 控制帧 = ISO 7816-4 APDU 风格: `CLA=0x89, INS, P1=0, P2=0, Lc, DATA`
- **成功响应 = `90 00`**（App 逐包校验 `resp[0]==0x90 && resp[1]==0x00`，失败即 sendFailed）
- IsoDep 超时: `setTimeout(200ms)`

## 3. NfcA 裸帧协议（VoltageTestActivity / writeKey）

ISO 14443-3A 层直接 transceive，帧格式 `0xAA + CMD + 参数`:

| 命令 | 字节 | 功能 | 响应 |
|---|---|---|---|
| 读电压 | `AA 22` | GET 电压 | 偏移4 == `0xA2` = 成功，偏移 5..8 为 4 字节电压数据（换算 mV） |
| 开负载 | `AA 31 01` | 开启测量负载 | `AA B1 ...`（0xAA + CMD\|0x80） |
| 关负载 | `AA 31 00` | 关闭负载 | 同上 |
| 改密钥 | `AA 11 01 02 03 04` | 修改认证 KEY | resp[1] == `0x81` = "0x81 接收成功" |

## 4. BLE 侧协议（对照，非 NFC）

`BitmapBleBmpManagement` 构建 BLE GATT 帧: 头 `0x85` + 4 字节长度 + 2 字节校验（种子 `0x82AC`，
`IntSplit` 系列=字节求和 mod 256 的校验和算法）+ 转义处理（`addBMP_RGB_888_Escape`）+ 帧尾 `0x95`，
`setConsumption` 维护滚动序号 toIndex（0..254 循环）。OTA 流程: OTA_ENTER → OTA_DATA_SEND → OTA_SAVE → OTA_CHECK。

## 5. 结论

1. **无 NDEF**。写入是 Witstec 专有协议：IsoDep 上跑 ISO 7816-4 APDU 风格帧（CLA=0x89），
   NfcA 上跑 0xAA 裸帧协议——同一标签两类接口并存（对应不同固件代次/功能）。
2. **协议按型号分流**：B042（4.2寸，400×300，200B/包×75）与 B037（3.7寸，240×416，
   240B/包×52）命令序列和帧格式完全不同，不可混用。
3. **B037 已真机验证**：握手 `EE 01` → `BB 10` → 52×数据帧 → `BB 13` → 52×数据帧 →
   `FF 01` 刷新，全程裸 89 帧、逐包 9000 应答、无尾包。图像为 1bpp 黑白 + 1bpp 红色
   双位图，行优先 MSB 在前，白/红像素 = 1。
4. BLE 与 NFC 共享业务语义（图像/OTA）但帧格式不同（0x85..0x95 带校验转义 vs APDU 9000）；
   `setConsumption` 双参/三参版本是 BLE 的，单参版本对 NFC 帧原样透传。