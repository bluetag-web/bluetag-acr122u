// Package tag — Witstec B037 (3.7寸, 240x416) 写卡协议
// 来源: jadx 反编译 MoreImageUpdateActivity.writeTag (B037 分支), 已真机验证
// 流程: 握手 EE 01 → BB 10 → 52x245B → BB 13 → 52x245B → FF 01 → 保场
// 注意: 无尾包 (App 中 91B 尾包分支是死代码, 实测标签拒绝)
package tag

import (
	"bytes"
	"fmt"
	"time"

	"bluetag-go/internal/pcsc"
	"bluetag-go/internal/pn532"
)

const (
	CLA = 0x89
	// 面板 240x416, 与 internal/imaging 中的 imgW/imgH/channelBytes 保持一致
	packetData        = 240 // 每包 240B (帧头长度字节 0xF0)
	packetsPerChannel = 52  // 52 * 240 = 12480
	channelBytes      = packetData * packetsPerChannel
)

var (
	cmdHandshake = []byte{CLA, 0xEE, 0x00, 0x00, 0x01, 0x01}
	cmdBWPrep    = []byte{CLA, 0xBB, 0x00, 0x00, 0x01, 0x10}
	cmdRedPrep   = []byte{CLA, 0xBB, 0x00, 0x00, 0x01, 0x13}
	cmdRefresh   = []byte{CLA, 0xFF, 0x00, 0x00, 0x01, 0x01}
)

// dataFrame: 第 index (0..51) 个 245B 数据帧: 89 CC 00 00 F0 + 240B
func dataFrame(ch []byte, index int) []byte {
	if index < 0 || index >= packetsPerChannel {
		panic("数据包序号越界")
	}
	f := make([]byte, 5, 5+packetData)
	f[0], f[1], f[2], f[3], f[4] = CLA, 0xCC, 0x00, 0x00, packetData
	return append(f, ch[index*packetData:(index+1)*packetData]...)
}

// ict: InCommunicateThru 发裸帧, 剥掉 PN532 整帧回显; 无响应返回 nil
func ict(card *pcsc.Card, frame []byte) []byte {
	status, tg, _ := pn532.InCommunicateThru(card, frame)
	if status != 0 || tg == nil {
		return nil
	}
	sent := frame
	if len(tg) >= len(sent) && bytes.Equal(tg[:len(sent)], sent) {
		tg = tg[len(sent):] // 整帧回显 → 剥掉
	} else if len(tg) <= len(sent) && bytes.Equal(sent[:len(tg)], tg) {
		tg = nil
	}
	return tg
}

// sendCmd: InDataExchange 发 89 命令帧, 校验标签响应 90 00, 重试 3 次
func sendCmd(card *pcsc.Card, frame []byte, retries int, log func(string)) error {
	for attempt := 0; attempt < retries; attempt++ {
		status, tg, err := pn532.InDataExchange(card, frame)
		if err != nil {
			log(fmt.Sprintf("[重试 %d/%d] 响应异常: %v", attempt+1, retries, err))
		} else if status != 0x00 {
			log(fmt.Sprintf("[重试 %d/%d] PN532 状态 %02X", attempt+1, retries, status))
		} else if len(tg) < 2 || tg[0] != 0x90 || tg[1] != 0x00 {
			log(fmt.Sprintf("[重试 %d/%d] 标签响应非 9000: % X", attempt+1, retries, tg))
		} else {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("命令 % X 失败", frame)
}

// sendDataPacket: 245B 数据帧, 单 I-block (PCB=0x02) 经 InCommunicateThru 直发
func sendDataPacket(card *pcsc.Card, packet []byte, retries int, log func(string)) error {
	frame := append([]byte{0x02}, packet...)
	for attempt := 0; attempt < retries; attempt++ {
		r := ict(card, frame)
		if r == nil || len(r) < 2 {
			log(fmt.Sprintf("[重试 %d/%d] 无响应", attempt+1, retries))
		} else {
			sw := r[len(r)-2:]
			if sw[0] == 0x90 && sw[1] == 0x00 {
				return nil
			}
			log(fmt.Sprintf("[重试 %d/%d] 标签 SW=% X", attempt+1, retries, r))
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("数据帧失败: % X...", packet[:6])
}

// sendChannel: 发送一个通道 52 x 245B 数据帧 (无尾包)
func sendChannel(card *pcsc.Card, ch []byte, phase string, retries int,
	progress func(phase string, done, total int), log func(string)) error {
	if len(ch) != channelBytes {
		return fmt.Errorf("通道数据长度应为 %d, 实际 %d", channelBytes, len(ch))
	}
	for i := 0; i < packetsPerChannel; i++ {
		if err := sendDataPacket(card, dataFrame(ch, i), retries, log); err != nil {
			return err
		}
		progress(phase, i+1, packetsPerChannel)
		time.Sleep(10 * time.Millisecond) // 略给标签喘息, 0 亦可
	}
	return nil
}

// Progress: 写卡进度回调 (phase: handshake/BW/RD/refresh/hold)
type Progress func(phase string, done, total int)

// handshakePhase: 握手 (含自动恢复)。首次写卡时标签/读卡器冷启动,
// InDataExchange 偶发返回空响应 (sw= data=), 与手动"再点一次写卡"等效:
// 重新断电复位 → 激活 → 再握手, 最多 3 轮。
func handshakePhase(card *pcsc.Card, prog Progress, log func(string)) error {
	var lastErr error
	for round := 0; round < 3; round++ {
		if round > 0 {
			log(fmt.Sprintf("握手失败 (第 %d 轮), 重新断电复位标签后重试...", round))
			if err := pn532.PowerCycle(card, 2, log); err != nil {
				return err
			}
			prog("activate", 0, 1)
			if err := pn532.Activate(card, log); err != nil {
				return err
			}
			time.Sleep(500 * time.Millisecond)
		}
		prog("handshake", 0, 1)
		lastErr = sendCmd(card, cmdHandshake, 3, log)
		if lastErr == nil {
			log("握手 89 EE 00 00 01 01 <- 9000 OK")
			return nil
		}
	}
	return lastErr
}

// WriteImage: 一站式写卡全流程 (阻塞, 约 20s)
func WriteImage(card *pcsc.Card, bw, rd []byte, hold time.Duration, prog Progress, log func(string)) error {
	if len(bw) != channelBytes || len(rd) != channelBytes {
		return fmt.Errorf("通道数据长度应为 %d", channelBytes)
	}
	prog("reset", 0, 1)
	if err := pn532.PowerCycle(card, 2, log); err != nil {
		return fmt.Errorf("断电复位失败: %w", err)
	}
	prog("activate", 0, 1)
	if err := pn532.Activate(card, log); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)

	prog("handshake", 0, 1)
	if err := handshakePhase(card, prog, log); err != nil {
		return err
	}

	prog("BW", 0, packetsPerChannel)
	if err := sendCmd(card, cmdBWPrep, 3, log); err != nil {
		return err
	}
	if err := sendChannel(card, bw, "BW", 3, prog, log); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond) // App: SystemClock.sleep(5ms)

	prog("RD", 0, packetsPerChannel)
	if err := sendCmd(card, cmdRedPrep, 3, log); err != nil {
		return err
	}
	if err := sendChannel(card, rd, "RD", 3, prog, log); err != nil {
		return err
	}

	prog("refresh", 0, 1)
	if err := sendCmd(card, cmdRefresh, 3, log); err != nil {
		return err
	}
	log("刷新 89 FF 00 00 01 01 <- 9000 OK")

	// 保场: 无源标签靠射频场供电刷新
	end := time.Now().Add(hold)
	for time.Now().Before(end) {
		prog("hold", int(time.Until(end).Seconds()), int(hold.Seconds()))
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}
