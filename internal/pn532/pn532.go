// Package pn532 — ACR122U 内置 PN532 直连 (Escape APDU 透传 PN532 原生命令)
// 对应 Python lib/transport.py
package pn532

import (
	"bytes"
	"fmt"
	"time"

	"bluetag-go/internal/pcsc"
)

// escapeAPDU: PN532 命令封装为 ACR122U Escape APDU (FF 00 00 00 len + cmd)
func escapeAPDU(cmd []byte) []byte {
	apdu := make([]byte, 5, 5+len(cmd))
	apdu[0], apdu[1], apdu[2], apdu[3], apdu[4] = 0xFF, 0x00, 0x00, 0x00, byte(len(cmd))
	return append(apdu, cmd...)
}

// pn532: 发 PN532 命令, 返回响应 (D5 xx ...), 重试 2 次
func pn532(card *pcsc.Card, cmd []byte) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		data, sw, err := card.Control(escapeAPDU(cmd))
		if err == nil && bytes.Equal(sw, []byte{0x90, 0x00}) && len(data) > 0 {
			return data, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("PN532 命令 % X 失败: sw=% X data=% X", cmd[:2], sw, data)
		}
		time.Sleep(300 * time.Millisecond)
	}
	return nil, lastErr
}

// fieldOff: 关闭读卡器射频场 (Set PICC Operating Field OFF + RFConfiguration)
func fieldOff(card *pcsc.Card) error {
	if _, _, err := card.Control([]byte{0xFF, 0x00, 0x41, 0x08, 0x00}); err != nil {
		return err
	}
	_, err := pn532(card, []byte{0xD4, 0x32, 0x01, 0x00}) // RFConfiguration: 场关闭
	return err
}

// PowerCycle: 关场 N 秒让标签断电复位
func PowerCycle(card *pcsc.Card, seconds int, log func(string)) error {
	if err := fieldOff(card); err != nil {
		return err
	}
	log(fmt.Sprintf("标签断电复位中 (%ds)...", seconds))
	time.Sleep(time.Duration(seconds) * time.Second)
	return nil
}

// Activate: InListPassiveTarget 开场并激活标签 (106kbps TypeA)
func Activate(card *pcsc.Card, log func(string)) error {
	rsp, err := pn532(card, []byte{0xD4, 0x4A, 0x01, 0x00})
	if err != nil {
		return err
	}
	if len(rsp) < 3 || rsp[0] != 0xD5 || rsp[1] != 0x4B {
		return fmt.Errorf("激活响应异常: % X", rsp)
	}
	if rsp[2] == 0 {
		return fmt.Errorf("未发现标签 (请把标签放到读卡器上)")
	}
	s := fmt.Sprintf("% X", rsp)
	if len(s) > 60 {
		s = s[:60] + "..."
	}
	log("标签已激活: " + s)
	return nil
}

// InDataExchange: D4 40 Tg=1 + 帧 → (状态, 标签响应)
func InDataExchange(card *pcsc.Card, frame []byte) (byte, []byte, error) {
	cmd := append([]byte{0xD4, 0x40, 0x01}, frame...)
	rsp, err := pn532(card, cmd)
	if err != nil {
		return 0xFF, nil, err
	}
	if len(rsp) < 2 || rsp[0] != 0xD5 || rsp[1] != 0x41 {
		return 0xFF, nil, fmt.Errorf("响应异常: % X", rsp)
	}
	return rsp[2], rsp[3:], nil
}

// InCommunicateThru: D4 42 裸帧直发 → (状态, 标签响应)
func InCommunicateThru(card *pcsc.Card, frame []byte) (byte, []byte, error) {
	cmd := append([]byte{0xD4, 0x42}, frame...)
	rsp, err := pn532(card, cmd)
	if err != nil {
		return 0xFF, nil, nil // 超时等错误 → 无响应 (对齐 Python ict 行为)
	}
	if len(rsp) < 3 || rsp[0] != 0xD5 || rsp[1] != 0x43 {
		return 0xFF, nil, nil
	}
	return rsp[2], rsp[3:], nil
}
