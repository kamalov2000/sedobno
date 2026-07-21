package imaging

import (
	"encoding/binary"
	"image"
)

// EXIF orientation: телефоны часто пишут пиксели «боком» и кладут в EXIF тег поворота
// (0x0112). Просмотрщики (галерея, Telegram) тег применяют — человек видит фото прямо.
// Стандартный image.Decode тег ИГНОРИРУЕТ: без коррекции vision-модель получала кадр
// боком, не читала повёрнутый текст и домысливала состав (доказанная причина галлюцинаций
// сканера на этикетках яиц). Здесь читаем тег и поворачиваем пиксели ДО ресайза, чтобы
// модель видела кадр так же, как человек. Тег есть только у JPEG; PNG/WebP → орентация 1.

// exifOrientation возвращает значение тега Orientation (1..8) из JPEG; 1 — нет поворота
// (или тег/EXIF отсутствует, или формат не JPEG). Парсер минимальный и защищён по границам:
// любая неполнота/битость → 1 (кадр остаётся как есть, хуже не делаем).
func exifOrientation(data []byte) int {
	// JPEG начинается с SOI FFD8. У PNG/WebP EXIF-ориентации по нашему пути нет.
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return 1
		}
		marker := data[i+1]
		// SOS (начало сжатых данных) или EOI — метаданных дальше нет.
		if marker == 0xDA || marker == 0xD9 {
			return 1
		}
		size := int(data[i+2])<<8 | int(data[i+3])
		if size < 2 || i+2+size > len(data) {
			return 1
		}
		seg := data[i+4 : i+2+size]
		// APP1 (FFE1) с сигнатурой "Exif\0\0" несёт TIFF-блок с тегами.
		if marker == 0xE1 && len(seg) >= 6 && string(seg[:6]) == "Exif\x00\x00" {
			return orientationFromTIFF(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

// orientationFromTIFF читает тег 0x0112 из IFD0 TIFF-блока (после сигнатуры Exif).
func orientationFromTIFF(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[0:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	// t[2:4] — магическое 0x002A; не проверяем строго, сразу к смещению IFD0.
	ifd := int(bo.Uint32(t[4:8]))
	if ifd < 8 || ifd+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[ifd:]))
	p := ifd + 2
	for k := 0; k < n; k++ {
		if p+12 > len(t) {
			return 1
		}
		tag := bo.Uint16(t[p:])
		if tag == 0x0112 { // Orientation, тип SHORT — значение в первых 2 байтах поля value.
			v := int(bo.Uint16(t[p+8:]))
			if v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
		p += 12
	}
	return 1
}

// applyOrientation поворачивает/отражает пиксели по EXIF-ориентации (1..8). Для 1 (или
// неизвестной) возвращает исходник без копии. Транспонирующие ориентации (5..8) меняют
// местами ширину и высоту.
//
// Раскладка EXIF: 1=как есть, 2=зеркало гориз., 3=180°, 4=зеркало верт.,
// 5=транспонирование, 6=90° по часовой, 7=поперечное, 8=90° против часовой.
func applyOrientation(src image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	swap := o >= 5 // транспонирующие меняют размер местами
	var dst *image.RGBA
	if swap {
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
	} else {
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.At(b.Min.X+x, b.Min.Y+y)
			var nx, ny int
			switch o {
			case 2:
				nx, ny = w-1-x, y // зеркало по горизонтали
			case 3:
				nx, ny = w-1-x, h-1-y // 180°
			case 4:
				nx, ny = x, h-1-y // зеркало по вертикали
			case 5:
				nx, ny = y, x // транспонирование (по главной диагонали)
			case 6:
				nx, ny = h-1-y, x // 90° по часовой
			case 7:
				nx, ny = h-1-y, w-1-x // поперечное (по побочной диагонали)
			case 8:
				nx, ny = y, w-1-x // 90° против часовой
			}
			dst.Set(nx, ny, c)
		}
	}
	return dst
}
