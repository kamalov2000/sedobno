package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestNormalize_ResizesAndReencodesToJPEG(t *testing.T) {
	// PNG 2000x1000 → длинная сторона 2000 > 1500 → ждём JPEG 1500x750.
	src := image.NewRGBA(image.Rect(0, 0, 2000, 1000))
	for y := 0; y < 1000; y++ {
		for x := 0; x < 2000; x++ {
			src.SetRGBA(x, y, color.RGBA{uint8(x), uint8(y), 120, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}

	out, err := Normalize(buf.Bytes(), 1500, 85)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	img, format, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("декодирование результата: %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("формат=%s, ждали jpeg", format)
	}
	if got := img.Bounds().Dx(); got != 1500 {
		t.Fatalf("ширина=%d, ждали 1500", got)
	}
	if got := img.Bounds().Dy(); got != 750 {
		t.Fatalf("высота=%d, ждали 750", got)
	}
}

func TestNormalize_SmallImageNotUpscaled(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 300, 200))
	var buf bytes.Buffer
	_ = png.Encode(&buf, src)
	out, err := Normalize(buf.Bytes(), 1500, 85)
	if err != nil {
		t.Fatal(err)
	}
	img, _, _ := image.Decode(bytes.NewReader(out))
	if img.Bounds().Dx() != 300 {
		t.Fatalf("ширина=%d, ждали 300 (без апскейла)", img.Bounds().Dx())
	}
}

func TestNormalize_BadData(t *testing.T) {
	if _, err := Normalize([]byte("это не картинка"), 1500, 85); err != ErrDecode {
		t.Fatalf("ждали ErrDecode, получили %v", err)
	}
}
