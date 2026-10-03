//go:build linux

// Package pcsc — libpcsclite 后端 (cgo): 与 pcsc_windows.go 导出 API 完全一致。
// 直连模式: SCARD_SHARE_DIRECT + SCardControl 发 Escape (PC/SC Part 10)。
//
// 编译前提: gcc + pkg-config + libpcsclite-dev (Debian/Ubuntu:
//
//	sudo apt install build-essential pkg-config libpcsclite-dev)
//
// 运行前提: pcscd 在跑且加载了读卡器 (systemctl status pcscd),
// CCID 驱动需允许 Escape (ACR122U + libccid 默认可用)。
package pcsc

/*
#cgo pkg-config: libpcsclite
#include <stdlib.h>
#include <winscard.h>
*/
import "C"

import (
	"encoding/binary"
	"fmt"
	"strings"
	"unsafe"
)

// pcsc-lite 头文件中的常量 (DWORD 为 unsigned long, 统一转 C.DWORD 传参)。
// SCARD_CTL_CODE(code) = 0x42000000 + code
const (
	scardShareDirect   = 0x0003            // SCARD_SHARE_DIRECT (无卡直连)
	scardLeaveCard     = 0x0000            // SCARD_LEAVE_CARD (对齐 Windows 版 disposition 0)
	ioctlGetFeatures   = 0x42000000 + 3400 // FEATURE_GET_FEATURE_REQUEST
	ioctlCcidEscapeDef = 0x42000000 + 3500 // CCID Escape 默认 IOCTL
	recvBufLen         = 1024
)

type Context struct{ h C.SCARDCONTEXT }
type Card struct {
	h     C.SCARDHANDLE
	ioctl uint32
}

func scardErr(what string, rc C.LONG) error {
	return fmt.Errorf("%s 失败 rc=0x%08X", what, uint32(rc))
}

// EstablishContext: 建立 PC/SC 资源管理器上下文 (pcscd)
func EstablishContext() (*Context, error) {
	var h C.SCARDCONTEXT
	rc := C.SCardEstablishContext(C.SCARD_SCOPE_USER, nil, nil, &h)
	if rc != 0 {
		return nil, scardErr("SCardEstablishContext", rc)
	}
	return &Context{h: h}, nil
}

// Release: 释放上下文
func (c *Context) Release() {
	if c != nil && c.h != 0 {
		C.SCardReleaseContext(c.h)
		c.h = 0
	}
}

// ListReaders 返回系统全部读卡器名 (multi-sz 解析)
func (c *Context) ListReaders() ([]string, error) {
	var n C.DWORD
	rc := C.SCardListReaders(c.h, nil, nil, &n)
	if rc != 0 && n == 0 {
		return nil, fmt.Errorf("未找到任何读卡器")
	}
	buf := make([]byte, n)
	rc = C.SCardListReaders(c.h, nil, (*C.char)(unsafe.Pointer(&buf[0])), &n)
	if rc != 0 {
		return nil, scardErr("SCardListReaders", rc)
	}
	var readers []string
	for _, s := range strings.Split(string(buf[:n]), "\x00") {
		if s != "" {
			readers = append(readers, s)
		}
	}
	if len(readers) == 0 {
		return nil, fmt.Errorf("未找到任何读卡器")
	}
	return readers, nil
}

// ConnectDirect 直连读卡器 (无卡模式), 并探测 Escape IOCTL
func (c *Context) ConnectDirect(reader string) (*Card, error) {
	rp := C.CString(reader)
	defer C.free(unsafe.Pointer(rp))
	var h C.SCARDHANDLE
	var proto C.DWORD
	rc := C.SCardConnect(c.h, rp, C.DWORD(scardShareDirect), C.DWORD(0), &h, &proto)
	if rc != 0 {
		return nil, scardErr("SCardConnect(DIRECT)", rc)
	}
	card := &Card{h: h, ioctl: ioctlCcidEscapeDef}
	if ioctl, ok := getEscapeIoctl(h); ok {
		card.ioctl = ioctl
	}
	return card, nil
}

// getEscapeIoctl: GET_FEATURE_REQUEST 解析 FEATURE_CCID_ESC_COMMAND (tag 0x0F)
func getEscapeIoctl(h C.SCARDHANDLE) (uint32, bool) {
	recv := make([]byte, recvBufLen)
	var ret C.DWORD
	rc := C.SCardControl(h, C.DWORD(ioctlGetFeatures), C.LPCVOID(nil), C.DWORD(0),
		C.LPVOID(unsafe.Pointer(&recv[0])), C.DWORD(recvBufLen), &ret)
	if rc != 0 || ret < 4 {
		return 0, false
	}
	raw := recv[:int(ret)]
	for i := 0; i+1 < len(raw); {
		tag, ln := raw[i], int(raw[i+1])
		if i+2+ln > len(raw) {
			break
		}
		if tag == 0x0F && ln == 4 { // FEATURE_CCID_ESC_COMMAND (PC/SC Part 10)
			return binary.LittleEndian.Uint32(raw[i+2 : i+6]), true
		}
		i += 2 + ln
	}
	return 0, false
}

// Disconnect: 断开读卡器直连
func (card *Card) Disconnect() {
	if card != nil && card.h != 0 {
		C.SCardDisconnect(card.h, C.DWORD(scardLeaveCard))
		card.h = 0
	}
}

// Control: SCardControl 发 Escape APDU, 返回 (data, sw两字节)
func (card *Card) Control(apdu []byte) ([]byte, []byte, error) {
	if len(apdu) == 0 {
		return nil, nil, fmt.Errorf("SCardControl: 空 APDU")
	}
	recv := make([]byte, recvBufLen)
	var ret C.DWORD
	rc := C.SCardControl(card.h, C.DWORD(card.ioctl),
		C.LPCVOID(unsafe.Pointer(&apdu[0])), C.DWORD(len(apdu)),
		C.LPVOID(unsafe.Pointer(&recv[0])), C.DWORD(recvBufLen), &ret)
	if rc != 0 {
		return nil, nil, scardErr("SCardControl", rc)
	}
	raw := recv[:int(ret)]
	if len(raw) < 2 {
		return nil, raw, nil
	}
	return raw[:len(raw)-2], raw[len(raw)-2:], nil
}
