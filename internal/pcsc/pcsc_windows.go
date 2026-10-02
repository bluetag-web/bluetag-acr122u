//go:build windows

// Package pcsc — winscard.dll 直接 syscall (无 cgo)
// 对应 Python emul_tag.py 的直连模式: SCARD_SHARE_DIRECT + Escape IOCTL
package pcsc

import (
	"encoding/binary"
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

var (
	modwinscard     = syscall.NewLazyDLL("winscard.dll")
	procEstablish   = modwinscard.NewProc("SCardEstablishContext")
	procListReaders = modwinscard.NewProc("SCardListReadersW")
	procConnect     = modwinscard.NewProc("SCardConnectW")
	procControl     = modwinscard.NewProc("SCardControl")
	procDisconnect  = modwinscard.NewProc("SCardDisconnect")
	procRelease     = modwinscard.NewProc("SCardReleaseContext")
)

const (
	scardScopeUser     = 0
	scardShareDirect   = 3
	ioctlGetFeatures   = 0x00313520
	ioctlCcidEscapeDef = 0x003136B0 // ACR122U 默认 Escape IOCTL
	recvBufLen         = 1024
)

type Context struct{ h uintptr }
type Card struct {
	h     uintptr
	ioctl uint32
}

func scardErr(what string, rc uintptr) error {
	return fmt.Errorf("%s 失败 rc=0x%08X", what, uint32(rc))
}

// EstablishContext: 建立 PC/SC 资源管理器上下文
func EstablishContext() (*Context, error) {
	var h uintptr
	r, _, _ := procEstablish.Call(scardScopeUser, 0, 0, uintptr(unsafe.Pointer(&h)))
	if r != 0 {
		return nil, scardErr("SCardEstablishContext", r)
	}
	return &Context{h: h}, nil
}

// Release: 释放上下文
func (c *Context) Release() {
	if c != nil && c.h != 0 {
		procRelease.Call(c.h)
	}
}

// ListReaders 返回系统全部读卡器名 (multi-sz 解析)
func (c *Context) ListReaders() ([]string, error) {
	var n uint32
	r, _, _ := procListReaders.Call(c.h, 0, 0, uintptr(unsafe.Pointer(&n)))
	if r == 0 && n == 0 {
		return nil, fmt.Errorf("未找到任何读卡器")
	}
	buf := make([]uint16, n)
	r, _, _ = procListReaders.Call(c.h, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if r != 0 {
		return nil, scardErr("SCardListReaders", r)
	}
	var readers []string
	cur := strings.Builder{}
	for _, u := range buf {
		if u == 0 {
			if cur.Len() > 0 {
				readers = append(readers, cur.String())
				cur.Reset()
			}
			if len(readers) > 0 && buf[0] == 0 { // 结尾双 0
				break
			}
			continue
		}
		cur.WriteRune(rune(u))
	}
	if len(readers) == 0 {
		return nil, fmt.Errorf("未找到任何读卡器")
	}
	return readers, nil
}

// ConnectDirect 直连读卡器 (无卡模式), 并探测 Escape IOCTL
func (c *Context) ConnectDirect(reader string) (*Card, error) {
	rp, err := syscall.UTF16PtrFromString(reader)
	if err != nil {
		return nil, err
	}
	var h uintptr
	var proto uint32
	r, _, _ := procConnect.Call(c.h, uintptr(unsafe.Pointer(rp)),
		scardShareDirect, 0, uintptr(unsafe.Pointer(&h)),
		uintptr(unsafe.Pointer(&proto)))
	if r != 0 {
		return nil, scardErr("SCardConnect(DIRECT)", r)
	}
	card := &Card{h: h, ioctl: ioctlCcidEscapeDef}
	if ioctl := c.getEscapeIoctl(h); ioctl != nil {
		card.ioctl = *ioctl
	}
	return card, nil
}

// getEscapeIoctl: GET_FEATURE_REQUEST 解析 FEATURE_CCID_ESCAPE (tag 0x02)
func (c *Context) getEscapeIoctl(h uintptr) *uint32 {
	recv := make([]byte, 1024)
	var ret uint32
	r, _, _ := procControl.Call(h, ioctlGetFeatures, 0, 0,
		uintptr(unsafe.Pointer(&recv[0])), recvBufLen,
		uintptr(unsafe.Pointer(&ret)))
	if r != 0 || ret < 4 {
		return nil
	}
	raw := recv[:ret]
	for i := 0; i+1 < len(raw); {
		tag, ln := raw[i], int(raw[i+1])
		if i+2+ln > len(raw) {
			break
		}
		if tag == 0x02 && ln == 4 {
			v := binary.LittleEndian.Uint32(raw[i+2 : i+6])
			return &v
		}
		i += 2 + ln
	}
	return nil
}

// Disconnect: 断开读卡器直连
func (card *Card) Disconnect() {
	if card != nil && card.h != 0 {
		procDisconnect.Call(card.h, 0)
	}
}

// Control: SCardControl 发 Escape APDU, 返回 (data, sw两字节)
func (card *Card) Control(apdu []byte) ([]byte, []byte, error) {
	recv := make([]byte, recvBufLen)
	var ret uint32
	r, _, _ := procControl.Call(card.h, uintptr(card.ioctl),
		uintptr(unsafe.Pointer(&apdu[0])), uintptr(len(apdu)),
		uintptr(unsafe.Pointer(&recv[0])), recvBufLen,
		uintptr(unsafe.Pointer(&ret)))
	if r != 0 {
		return nil, nil, scardErr("SCardControl", r)
	}
	raw := recv[:ret]
	if len(raw) < 2 {
		return nil, raw, nil
	}
	return raw[:len(raw)-2], raw[len(raw)-2:], nil
}
