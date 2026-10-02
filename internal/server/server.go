// Package server — B037 价签写卡 Web 服务 HTTP 层
//   GET  /            内置网页 (web.IndexHTML, go:embed)
//   GET  /api/status  读卡器状态
//   POST /api/upload  上传图片 → 处理 → JSON (统计 + base64 预览)
//   POST /api/write   上传前端已处理好的 240x416 三色图 → 校验 → 写卡, NDJSON 流式进度
package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"bluetag-go/internal/imaging"
	"bluetag-go/internal/pcsc"
	"bluetag-go/internal/tag"
	"bluetag-go/web"
)

const listenAddr = "127.0.0.1:8765"

var (
	writeMu sync.Mutex // 同一时间只允许一个写卡任务
	busy    bool
)

// Run: 启动 HTTP 服务 (阻塞)
func Run() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(web.IndexHTML)
	})
	mux.HandleFunc("/api/status", handleStatus)
	mux.HandleFunc("/api/upload", handleUpload)
	mux.HandleFunc("/api/write", handleWrite)
	log.Printf("B037 价签写卡服务: http://%s (仅本机监听)", listenAddr)
	// 仅监听回环地址: 写卡服务涉及本机 USB 读卡器, 不对局域网/公网暴露。
	// 远程在线设计器页面在浏览器中跨域调用本机 API, 不受影响 (CORS 已开启)。
	return http.ListenAndServe(listenAddr, cors(mux))
}

// cors: 跨域中间件 — 供部署在远程服务器上的在线设计器调用 API。
// 默认 Access-Control-Allow-Origin: * ; 如需限制来源,
// 启动前设置环境变量 CORS_ORIGIN (如 https://designer.example.com)。
// 预检 OPTIONS 直接返回 204。
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := os.Getenv("CORS_ORIGIN")
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withCard: 建上下文 → 直连读卡器 → 执行 fn → 清理
func withCard(fn func(card *pcsc.Card) error) error {
	ctx, err := pcsc.EstablishContext()
	if err != nil {
		return err
	}
	defer ctx.Release()
	readers, err := ctx.ListReaders()
	if err != nil {
		return err
	}
	reader := pickReader(readers)
	card, err := ctx.ConnectDirect(reader)
	if err != nil {
		return err
	}
	defer card.Disconnect()
	log.Println("使用读卡器:", reader)
	return fn(card)
}

func pickReader(readers []string) string {
	for _, r := range readers {
		if containsUpper(r, "ACR") {
			return r
		}
	}
	return readers[0]
}

func containsUpper(s, sub string) bool {
	return strings.Contains(strings.ToUpper(s), sub)
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	out := map[string]any{"busy": busy}
	ctx, err := pcsc.EstablishContext()
	if err == nil {
		defer ctx.Release()
		if readers, err := ctx.ListReaders(); err == nil {
			out["reader"] = pickReader(readers)
			out["ok"] = true
		} else {
			out["ok"] = false
			out["error"] = err.Error()
		}
	} else {
		out["ok"] = false
		out["error"] = err.Error()
	}
	json.NewEncoder(w).Encode(out)
}

// parseParams: 从 multipart 表单取图片处理参数
func parseParams(r *http.Request) imaging.Params {
	geti := func(name string, def int) int {
		if v, err := strconv.Atoi(r.FormValue(name)); err == nil {
			return v
		}
		return def
	}
	return imaging.Params{
		Stretch:   r.FormValue("stretch") == "true",
		Threshold: geti("threshold", 128),
		RedMin:    geti("redMin", 120),
		RedDiff:   geti("redDiff", 40),
		Dither:    r.FormValue("dither") == "true",
	}
}

// readImageFile: 从 multipart 表单取图片文件字节
func readImageFile(r *http.Request) ([]byte, error) {
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		return nil, err
	}
	f, _, err := r.FormFile("image")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func readImage(r *http.Request) ([]byte, imaging.Params, error) {
	data, err := readImageFile(r)
	if err != nil {
		return nil, imaging.Params{}, err
	}
	return data, parseParams(r), nil
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	data, p, err := readImage(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	bw, rd, white, black, red, preview, err := imaging.ProcessImage(data, p)
	if err != nil {
		http.Error(w, "图片处理失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"white": white, "black": black, "red": red,
		"bwLen": len(bw), "rdLen": len(rd),
		"preview": "data:image/png;base64," +
			base64.StdEncoding.EncodeToString(preview),
	})
}

// writeEvent: 写一行 NDJSON 并立即 flush
func writeEvent(w http.ResponseWriter, v map[string]any) {
	b, _ := json.Marshal(v)
	w.Write(b)
	w.Write([]byte("\n"))
	w.(http.Flusher).Flush()
}

func handleWrite(w http.ResponseWriter, r *http.Request) {
	writeMu.Lock()
	if busy {
		writeMu.Unlock()
		http.Error(w, "已有写卡任务进行中", http.StatusConflict)
		return
	}
	busy = true
	writeMu.Unlock()
	defer func() { busy = false }()

	data, err := readImageFile(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// /api/write 不做任何图像处理 (缩放/红判定/二值化由前端完成),
	// 只校验尺寸与纯三色; 镜像在 ValidateImage 打包层完成。
	// 旧的处理参数 (threshold/redMin/redDiff/dither/stretch) 收到即忽略。
	bw, rd, _, _, _, err := imaging.ValidateImage(data)
	if err != nil {
		http.Error(w, "图片校验失败: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")

	logFn := func(msg string) {
		writeEvent(w, map[string]any{"type": "log", "msg": msg})
	}
	prog := func(phase string, done, total int) {
		writeEvent(w, map[string]any{
			"type": "progress", "phase": phase, "done": done, "total": total,
		})
	}

	start := time.Now()
	err = withCard(func(card *pcsc.Card) error {
		return tag.WriteImage(card, bw, rd, 15*time.Second, prog, logFn)
	})
	if err != nil {
		writeEvent(w, map[string]any{"type": "error", "msg": err.Error()})
		return
	}
	writeEvent(w, map[string]any{
		"type": "done", "seconds": fmt.Sprintf("%.1f", time.Since(start).Seconds()),
	})
}
