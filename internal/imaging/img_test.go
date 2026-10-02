package imaging

// img_test.go — ValidateImage 校验与镜像打包测试 (go test)

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// makePNG: 生成指定尺寸的测试 PNG; fn 返回像素颜色
func makePNG(t *testing.T, w, h int, fn func(x, y int) color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, fn(x, y))
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

// TestValidateImageOK: 合法三色图 → 校验通过, 镜像打包字节正确
func TestValidateImageOK(t *testing.T) {
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}
	red := color.RGBA{255, 0, 0, 255}
	data := makePNG(t, imgW, imgH, func(x, y int) color.RGBA {
		switch {
		case x == 0 && y == 0:
			return black // 原图最左上 → 打包后应在行尾 (mx=239)
		case x == imgW-1 && y == 0:
			return red // 原图最右上 → 打包后应在行首 (mx=0)
		default:
			return white
		}
	})

	bw, rd, whiteN, blackN, redN, err := ValidateImage(data)
	if err != nil {
		t.Fatalf("ValidateImage 失败: %v", err)
	}
	if len(bw) != channelBytes || len(rd) != channelBytes {
		t.Fatalf("通道长度错误: bw=%d rd=%d 期望 %d", len(bw), len(rd), channelBytes)
	}
	if blackN != 1 || redN != 1 || whiteN != imgW*imgH-1 {
		t.Fatalf("统计错误: white=%d black=%d red=%d", whiteN, blackN, redN)
	}

	// 第 0 行: 除 mx=0 (红, BW 计白 + RD 置位) 外全白
	if rd[0] != 0x80 {
		t.Errorf("rd[0]=%02X 期望 80 (红像素镜像到行首)", rd[0])
	}
	if bw[0] != 0xFF {
		t.Errorf("bw[0]=%02X 期望 FF (含红像素计白)", bw[0])
	}
	// 行尾字节: mx=239 (原图 x=0 黑) 对应 0x01 位应清除
	if bw[rowBytes-1] != 0xFE {
		t.Errorf("bw[29]=%02X 期望 FE (黑像素镜像到行尾)", bw[rowBytes-1])
	}
	// 第 1 行无红: rd 全 0
	for i := 0; i < rowBytes; i++ {
		if rd[rowBytes+i] != 0 {
			t.Fatalf("rd 第 1 行应全 0, 字节 %d = %02X", i, rd[rowBytes+i])
		}
	}
}

// TestValidateImageTolerance: 容差内颜色 (±16) 应通过
func TestValidateImageTolerance(t *testing.T) {
	offWhite := color.RGBA{242, 244, 240, 255}
	offRed := color.RGBA{240, 8, 12, 255}
	data := makePNG(t, imgW, imgH, func(x, y int) color.RGBA {
		if x < imgW/2 {
			return offWhite
		}
		return offRed
	})
	if _, _, _, _, _, err := ValidateImage(data); err != nil {
		t.Fatalf("容差内颜色被拒绝: %v", err)
	}
}

// TestValidateImageWrongSize: 尺寸不对应报错
func TestValidateImageWrongSize(t *testing.T) {
	white := color.RGBA{255, 255, 255, 255}
	data := makePNG(t, 100, 100, func(x, y int) color.RGBA { return white })
	_, _, _, _, _, err := ValidateImage(data)
	if err == nil {
		t.Fatal("尺寸 100x100 应报错")
	}
	if !strings.Contains(err.Error(), "240x416") {
		t.Errorf("错误信息应包含期望尺寸, 实际: %v", err)
	}
}

// TestValidateImageBadPixel: 非三色像素报错并带坐标
func TestValidateImageBadPixel(t *testing.T) {
	white := color.RGBA{255, 255, 255, 255}
	gray := color.RGBA{128, 128, 128, 255}
	data := makePNG(t, imgW, imgH, func(x, y int) color.RGBA {
		if x == 123 && y == 45 {
			return gray
		}
		return white
	})
	_, _, _, _, _, err := ValidateImage(data)
	if err == nil {
		t.Fatal("含灰色像素应报错")
	}
	if !strings.Contains(err.Error(), "x=123") || !strings.Contains(err.Error(), "y=45") {
		t.Errorf("错误信息应包含违规像素坐标, 实际: %v", err)
	}
}

// TestValidateImageAlpha: 透明像素报错
func TestValidateImageAlpha(t *testing.T) {
	transparent := color.RGBA{0, 0, 0, 0}
	data := makePNG(t, imgW, imgH, func(x, y int) color.RGBA { return transparent })
	_, _, _, _, _, err := ValidateImage(data)
	if err == nil {
		t.Fatal("全透明图应报错")
	}
	if !strings.Contains(err.Error(), "透明度") {
		t.Errorf("错误信息应提示透明度, 实际: %v", err)
	}
}

// TestValidateImageNotThreeColor: 多个违规像素时列出前 3 个
func TestValidateImageNotThreeColor(t *testing.T) {
	white := color.RGBA{255, 255, 255, 255}
	blue := color.RGBA{0, 0, 255, 255}
	data := makePNG(t, imgW, imgH, func(x, y int) color.RGBA {
		if y < 5 {
			return blue
		}
		return white
	})
	_, _, _, _, _, err := ValidateImage(data)
	if err == nil {
		t.Fatal("含蓝色像素应报错")
	}
	// 240 * 5 = 1200 个违规, 应只列出前 3 个
	if !strings.Contains(err.Error(), "前 3 个") {
		t.Errorf("错误信息应说明只列出前 3 个, 实际: %v", err)
	}
	if n := strings.Count(err.Error(), "(x="); n != 3 {
		t.Errorf("应列出 3 个违规像素, 实际 %d 个: %v", n, err)
	}
}
