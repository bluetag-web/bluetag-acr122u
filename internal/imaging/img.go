// Package imaging — 图片 → B037 双通道位图 (BW/RD 各 12480B)
// 对应 Python write_image.py: 缩放(等比居中/拉伸) + 红判定 + FS 抖动 + 打包
package imaging

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"
	"strings"
)

const (
	imgW = 240 // 面板宽
	imgH = 416 // 面板高

	rowBytes     = imgW / 8        // 每行 30B
	channelBytes = rowBytes * imgH // 双通道各 12480B
)

// Params: 图像处理参数
type Params struct {
	Stretch   bool // 拉伸铺满 (false=等比缩放白底居中)
	Threshold int  // 黑白阈值 (灰度 >= T 为白)
	RedMin    int  // 红色判定: R 最小值
	RedDiff   int  // 红色判定: R - max(G,B) >= D
	Dither    bool // Floyd-Steinberg 抖动
}

// decodeRGBA: 解码任意已注册格式 (png/jpeg/gif)
func decodeRGBA(data []byte) (*image.RGBA, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dst.Set(x-b.Min.X, y-b.Min.Y, src.At(x, y))
		}
	}
	return dst, nil
}

// resizeBilinear: 手写双线性缩放
func resizeBilinear(src *image.RGBA, dw, dh int) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	fx := float64(sw) / float64(dw)
	fy := float64(sh) / float64(dh)
	for y := 0; y < dh; y++ {
		gy := (float64(y)+0.5)*fy - 0.5
		y0 := int(math.Floor(gy))
		ty := gy - float64(y0)
		y0c, y1c := clamp(y0, sh), clamp(y0+1, sh)
		for x := 0; x < dw; x++ {
			gx := (float64(x)+0.5)*fx - 0.5
			x0 := int(math.Floor(gx))
			tx := gx - float64(x0)
			x0c, x1c := clamp(x0, sw), clamp(x0+1, sw)
			// 逐通道双线性
			v00, v10 := chanAt(src, x0c, y0c), chanAt(src, x1c, y0c)
			v01, v11 := chanAt(src, x0c, y1c), chanAt(src, x1c, y1c)
			out := make([]uint8, 3)
			for c := 0; c < 3; c++ {
				top := float64(v00[c])*(1-tx) + float64(v10[c])*tx
				bot := float64(v01[c])*(1-tx) + float64(v11[c])*tx
				out[c] = uint8(clampf(top*(1-ty)+bot*ty+0.5, 0, 255))
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = out[0], out[1], out[2], 255
		}
	}
	return dst
}

func chanAt(src *image.RGBA, x, y int) [3]uint8 {
	i := src.PixOffset(x, y)
	return [3]uint8{src.Pix[i], src.Pix[i+1], src.Pix[i+2]}
}

func clamp(v, max int) int {
	if v < 0 {
		return 0
	}
	if v >= max {
		return max - 1
	}
	return v
}

func clampf(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// prepare: 缩放到 240x416 (不做镜像 — 预览保持原图方向;
// 面板 x 方向的镜像在打包时处理)
func prepare(src *image.RGBA, p Params) *image.RGBA {
	if p.Stretch {
		return resizeBilinear(src, imgW, imgH)
	}
	scale := math.Min(float64(imgW)/float64(src.Bounds().Dx()),
		float64(imgH)/float64(src.Bounds().Dy()))
	nw := int(float64(src.Bounds().Dx())*scale + 0.5)
	nh := int(float64(src.Bounds().Dy())*scale + 0.5)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	resized := resizeBilinear(src, nw, nh)
	canvas := image.NewRGBA(image.Rect(0, 0, imgW, imgH))
	for i := range canvas.Pix { // 白底
		canvas.Pix[i] = 255
	}
	ox, oy := (imgW-nw)/2, (imgH-nh)/2
	for y := 0; y < nh; y++ {
		copy(canvas.Pix[canvas.PixOffset(ox, oy+y):canvas.PixOffset(ox, oy+y)+nw*4],
			resized.Pix[resized.PixOffset(0, y):resized.PixOffset(0, y)+nw*4])
	}
	return canvas
}

// ProcessImage: 图片字节 → (bw, rd, 统计, 预览PNG)
func ProcessImage(data []byte, p Params) (bw, rd []byte,
	white, black, red int, preview []byte, err error) {
	src, err := decodeRGBA(data)
	if err != nil {
		return nil, nil, 0, 0, 0, nil, err
	}
	canvas := prepare(src, p)

	// 红色掩码 + 灰度
	redM := make([][]bool, imgH)
	gray := make([][]float64, imgH)
	for y := 0; y < imgH; y++ {
		redM[y] = make([]bool, imgW)
		gray[y] = make([]float64, imgW)
		for x := 0; x < imgW; x++ {
			c := canvas.RGBAAt(x, y)
			r, g, b := float64(c.R), float64(c.G), float64(c.B)
			redM[y][x] = r >= float64(p.RedMin) && r-max(g, b) >= float64(p.RedDiff)
			gray[y][x] = 0.299*r + 0.587*g + 0.114*b
		}
	}

	// 白色图 (true=白)
	whiteM := make([][]bool, imgH)
	for y := range whiteM {
		whiteM[y] = make([]bool, imgW)
	}
	if p.Dither {
		ditherFS(gray, redM, whiteM)
	} else {
		t := float64(p.Threshold)
		for y := 0; y < imgH; y++ {
			for x := 0; x < imgW; x++ {
				whiteM[y][x] = redM[y][x] || gray[y][x] >= t
			}
		}
	}

	bw = make([]byte, channelBytes)
	rd = make([]byte, channelBytes)
	for y := 0; y < imgH; y++ {
		for x := 0; x < imgW; x++ {
			// 面板行扫描 x 方向与数据相反: 打包时镜像 (真机验证)。
			// 预览图 (whiteM/redM) 保持原图方向。
			mx := imgW - 1 - x
			if whiteM[y][x] {
				bw[y*rowBytes+mx>>3] |= 0x80 >> (mx & 7)
				white++
			} else {
				black++
			}
			if redM[y][x] {
				rd[y*rowBytes+mx>>3] |= 0x80 >> (mx & 7)
				red++
			}
		}
	}

	preview, err = renderPreview(whiteM, redM)
	return bw, rd, white, black, red, preview, err
}

// ditherFS: Floyd-Steinberg 抖动 (红像素固定白, 不参与误差扩散)
func ditherFS(gray [][]float64, redM, whiteM [][]bool) {
	for y := 0; y < imgH; y++ {
		for x := 0; x < imgW; x++ {
			if redM[y][x] {
				whiteM[y][x] = true
				continue
			}
			old := gray[y][x]
			newV := 255.0
			if old < 128 {
				newV = 0
			}
			whiteM[y][x] = newV == 255
			errd := old - newV
			for _, d := range [][3]float64{{1, 0, 7 / 16}, {-1, 1, 3 / 16},
				{0, 1, 5 / 16}, {1, 1, 1 / 16}} {
				nx, ny := x+int(d[0]), y+int(d[1])
				if nx >= 0 && nx < imgW && ny >= 0 && ny < imgH && !redM[ny][nx] {
					gray[ny][nx] += errd * d[2]
				}
			}
		}
	}
}

// renderPreview: 白/黑/红三色模拟图 → PNG 字节
func renderPreview(whiteM, redM [][]bool) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, imgW, imgH))
	whiteC := color.RGBA{255, 255, 255, 255}
	blackC := color.RGBA{0, 0, 0, 255}
	redC := color.RGBA{237, 28, 36, 255}
	for y := 0; y < imgH; y++ {
		for x := 0; x < imgW; x++ {
			c := blackC
			if redM[y][x] {
				c = redC
			} else if whiteM[y][x] {
				c = whiteC
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// colorTol: 三色校验的每通道容差 (容忍 PNG 编码器 / ICC 色彩管理的微小扰动)
const colorTol = 16

func near(v, target int) bool {
	d := v - target
	if d < 0 {
		d = -d
	}
	return d <= colorTol
}

// ValidateImage: 校验前端已处理好的图片 (240x416 纯三色) 并打包为双通道位图。
// 不做缩放 / 红判定 / 二值化 — 这些由前端完成; 镜像仍在打包层完成。
// 违规时返回带具体像素坐标的错误。
func ValidateImage(data []byte) (bw, rd []byte, white, black, red int, err error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil, 0, 0, 0, fmt.Errorf("图片解码失败: %w", err)
	}
	b := src.Bounds()
	if b.Dx() != imgW || b.Dy() != imgH {
		return nil, nil, 0, 0, 0, fmt.Errorf(
			"图片尺寸必须为 %dx%d, 实际 %dx%d", imgW, imgH, b.Dx(), b.Dy())
	}

	bw = make([]byte, channelBytes)
	rd = make([]byte, channelBytes)
	var bad []string // 收集前 3 个违规像素
	for y := 0; y < imgH; y++ {
		for x := 0; x < imgW; x++ {
			c := color.NRGBAModel.Convert(src.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			r, g, bl := int(c.R), int(c.G), int(c.B)
			okColor := false
			if c.A == 255 {
				switch {
				case near(r, 255) && near(g, 255) && near(bl, 255): // 白
					okColor = true
				case near(r, 0) && near(g, 0) && near(bl, 0): // 黑
					okColor = true
				case near(r, 255) && near(g, 0) && near(bl, 0): // 红
					okColor = true
				}
			}
			if !okColor {
				if c.A != 255 {
					bad = append(bad, fmt.Sprintf(
						"(x=%d,y=%d) 含透明度 alpha=%d (仅允许不透明的纯白/纯黑/纯红)", x, y, c.A))
				} else {
					bad = append(bad, fmt.Sprintf(
						"(x=%d,y=%d)=RGB(%d,%d,%d) 不是纯白/纯黑/纯红", x, y, r, g, bl))
				}
				if len(bad) >= 3 {
					return nil, nil, 0, 0, 0, fmt.Errorf("图片含非法像素: %s (共列出前 %d 个)",
						strings.Join(bad, "; "), len(bad))
				}
				continue
			}

			// 面板行扫描 x 方向与数据相反: 打包时镜像 (与 ProcessImage 一致)。
			mx := imgW - 1 - x
			isRed := near(r, 255) && near(g, 0) && near(bl, 0)
			if isRed {
				rd[y*rowBytes+mx>>3] |= 0x80 >> (mx & 7)
				red++
			}
			if isRed || (near(r, 255) && near(g, 255) && near(bl, 255)) {
				// 白像素; 红像素在 BW 通道计白 (与原服务端处理行为一致)
				bw[y*rowBytes+mx>>3] |= 0x80 >> (mx & 7)
				white++
			} else {
				black++
			}
		}
	}
	if len(bad) > 0 {
		return nil, nil, 0, 0, 0, fmt.Errorf("图片含非法像素: %s", strings.Join(bad, "; "))
	}
	return bw, rd, white, black, red, nil
}
