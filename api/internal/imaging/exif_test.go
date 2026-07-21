package imaging

import (
	"bytes"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

// exifOrientation читает тег из реальных JPEG-фикстур (сгенерированы PIL с тегами 1/3/6/8
// и один без EXIF). Без тега/не-JPEG → 1.
func TestExifOrientation(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "testdata", "imaging")
	cases := map[string]int{
		"orient_1.jpg": 1, "orient_3.jpg": 3, "orient_6.jpg": 6, "orient_8.jpg": 8,
		"orient_none.jpg": 1, // нет EXIF → 1
	}
	for file, want := range cases {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatalf("нет фикстуры %s: %v", file, err)
		}
		if got := exifOrientation(data); got != want {
			t.Errorf("exifOrientation(%s)=%d, ждали %d", file, got, want)
		}
	}
	// Мусор / не-JPEG не должен паниковать и даёт 1.
	if exifOrientation([]byte{0x89, 'P', 'N', 'G'}) != 1 {
		t.Error("PNG-сигнатура должна дать orientation 1")
	}
	if exifOrientation(nil) != 1 || exifOrientation([]byte{0xFF}) != 1 {
		t.Error("битые данные должны дать 1, а не паниковать")
	}
}

// applyOrientation — все 8 ориентаций на различимой 3×2 картинке. Проверяем: transpose'ы
// (5..8) меняют размер местами, и маркер-пиксель (0,0)=белый едет в правильный угол.
func TestApplyOrientation(t *testing.T) {
	// 3×2, белый левый-верх, чёрный правый-низ, остальное серое.
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			src.Set(x, y, color.RGBA{128, 128, 128, 255})
		}
	}
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}
	src.Set(0, 0, white)
	src.Set(2, 1, black)

	at := func(img image.Image, x, y int) color.RGBA {
		r, g, b, a := img.At(x, y).RGBA()
		return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
	}

	// ожидаемые: размер (w,h) и куда уехал белый маркер из (0,0).
	type exp struct {
		w, h   int
		wx, wy int // белый
	}
	cases := map[int]exp{
		1: {3, 2, 0, 0},
		2: {3, 2, 2, 0}, // зеркало гориз: (0,0)→(w-1,0)
		3: {3, 2, 2, 1}, // 180: (0,0)→(w-1,h-1)
		4: {3, 2, 0, 1}, // зеркало верт
		5: {2, 3, 0, 0}, // транспонирование: (0,0)→(0,0), размер swap
		6: {2, 3, 1, 0}, // 90 CW: (0,0)→(h-1,0)=(1,0)
		7: {2, 3, 1, 2}, // поперечное
		8: {2, 3, 0, 2}, // 90 CCW: (0,0)→(0,w-1)=(0,2)
	}
	for o, e := range cases {
		dst := applyOrientation(src, o)
		bb := dst.Bounds()
		if bb.Dx() != e.w || bb.Dy() != e.h {
			t.Errorf("o=%d размер %dx%d, ждали %dx%d", o, bb.Dx(), bb.Dy(), e.w, e.h)
			continue
		}
		if c := at(dst, e.wx, e.wy); c != white {
			t.Errorf("o=%d белый маркер не в (%d,%d): %v", o, e.wx, e.wy, c)
		}
	}
	// o=1 возвращает исходник без копии (тот же указатель) — no-op дёшев.
	if applyOrientation(src, 1) != image.Image(src) {
		t.Error("o=1 должен вернуть исходник без изменений")
	}
}

// Полный путь Normalize: кадр с тегом 6 (90° CW) поворачивается — длинная/короткая
// стороны меняются местами; кадр без тега остаётся как есть.
func TestNormalize_AppliesOrientation(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "testdata", "imaging")
	// orient_6.jpg — 8×4 пикселя, тег 6 (90° CW) → после коррекции 4×8.
	data, err := os.ReadFile(filepath.Join(dir, "orient_6.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Normalize(data, 0, 90) // maxDim=0 — без ресайза, чистый эффект поворота
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 4 || img.Bounds().Dy() != 8 {
		t.Errorf("orient_6 после Normalize %dx%d, ждали 4x8 (повёрнут)", img.Bounds().Dx(), img.Bounds().Dy())
	}

	// Без EXIF — размеры не меняются (8×4).
	none, _ := os.ReadFile(filepath.Join(dir, "orient_none.jpg"))
	out2, err := Normalize(none, 0, 90)
	if err != nil {
		t.Fatal(err)
	}
	img2, _, _ := image.Decode(bytes.NewReader(out2))
	if img2.Bounds().Dx() != 8 || img2.Bounds().Dy() != 4 {
		t.Errorf("orient_none после Normalize %dx%d, ждали 8x4 (без поворота)", img2.Bounds().Dx(), img2.Bounds().Dy())
	}
}
