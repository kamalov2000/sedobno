// Package imaging нормализует загруженное фото перед отправкой в vision-модель
// (§8): декодирует JPEG/PNG/WebP, ужимает до предела по длинной стороне и
// перекодирует в JPEG. Меньше пикселей → меньше image-токенов и стоимость скана.
// HEIC не поддерживается на сервере (нет pure-Go декодера) — нормализуется на
// клиенте в JPEG; сюда доходит уже поддерживаемый формат.
package imaging

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png" // регистрация PNG-декодера

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // регистрация WebP-декодера
)

// ErrDecode — формат не распознан (не JPEG/PNG/WebP или битые данные).
var ErrDecode = errors.New("не удалось распознать изображение")

// Normalize декодирует изображение, при необходимости ужимает до maxDim по длинной
// стороне (высококачественный CatmullRom) и кодирует в JPEG с заданным quality.
func Normalize(data []byte, maxDim, quality int) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrDecode
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, ErrDecode
	}
	if maxDim > 0 && (w > maxDim || h > maxDim) {
		nw, nh := scaledDims(w, h, maxDim)
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
		img = dst
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// scaledDims сохраняет пропорции: длинная сторона = maxDim.
func scaledDims(w, h, maxDim int) (int, int) {
	if w >= h {
		return maxDim, int(float64(h) * float64(maxDim) / float64(w))
	}
	return int(float64(w) * float64(maxDim) / float64(h)), maxDim
}
