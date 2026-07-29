package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// solidJPEG — кадр одного цвета: краёв в нём нет по построению.
func solidJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 128, G: 128, B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatalf("кодирование: %v", err)
	}
	return buf.Bytes()
}

// corpusDir — общий корпус проекта. Тесты идут против НАСТОЯЩИХ кадров, а не
// сгенерированных шумов: метрика меряет «мелкий шрифт на грани читаемости», и
// синтетика такого свойства не воспроизводит.
const corpusDir = "../../../testdata"

func measureFile(t *testing.T, path string) Density {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("нет файла %s: %v", path, err)
	}
	// Меряем ПО НОРМАЛИЗОВАННОМУ кадру — ровно как в проде (scan.go нормализует
	// перед отправкой в модель и перед замером).
	norm, err := Normalize(raw, 1500, 85)
	if err != nil {
		t.Fatalf("нормализация %s: %v", path, err)
	}
	d, err := Measure(norm)
	if err != nil {
		t.Fatalf("замер %s: %v", path, err)
	}
	return d
}

// Онигири — кадр, на котором дешёвая модель четыре раза подряд СОЧИНИЛА состав
// (81/38/68/74, верен только эскалированный). Он обязан оставаться самым «мелким»
// в корпусе: если правка ресайза или порога это сломает, форс-эскалация перестанет
// на нём срабатывать молча.
func TestMeasure_ОнигириСамыйМелкийВКорпусе(t *testing.T) {
	onigiri := filepath.Join(corpusDir, "_beta_findings", "2026-07-27_onigiri-snezhny-krab__beta-scan-81-38-68-74.jpg")
	got := measureFile(t, onigiri)

	if got.FineLoss < ForceEscalateFineLoss {
		t.Errorf("fine_loss онигири = %.3f, ниже порога форс-эскалации %.2f — кейс перестал ловиться",
			got.FineLoss, ForceEscalateFineLoss)
	}

	// И он же должен быть максимумом среди обычных этикеток: метрика обязана
	// РАЗДЕЛЯТЬ, а не просто выдавать большое число на всём подряд.
	labels, _ := filepath.Glob(filepath.Join(corpusDir, "labels", "*.jpg"))
	if len(labels) == 0 {
		t.Skip("корпус этикеток недоступен")
	}
	var maxOther float64
	var maxName string
	for _, p := range labels {
		d := measureFile(t, p)
		if d.FineLoss > maxOther {
			maxOther, maxName = d.FineLoss, filepath.Base(p)
		}
	}
	if got.FineLoss <= maxOther {
		t.Errorf("fine_loss онигири = %.3f не больше максимума обычных этикеток (%.3f, %s)",
			got.FineLoss, maxOther, maxName)
	}
	t.Logf("онигири %.3f против максимума обычных %.3f (%s)", got.FineLoss, maxOther, maxName)
}

// Ровная заливка деталей не имеет — метрика обязана давать 0, а не делить на ноль.
func TestMeasure_РовныйКадрДаётНоль(t *testing.T) {
	flat := solidJPEG(t, 200, 150)
	got, err := Measure(flat)
	if err != nil {
		t.Fatalf("замер: %v", err)
	}
	if got.EdgeFrac != 0 || got.FineLoss != 0 {
		t.Errorf("ровный кадр: edge=%.4f fine_loss=%.3f, ждали нули", got.EdgeFrac, got.FineLoss)
	}
}

func TestMeasure_БитыйКадрНеПаникует(t *testing.T) {
	if _, err := Measure([]byte("не картинка")); err == nil {
		t.Error("ждали ошибку декодирования, получили nil")
	}
}

// Диагностика: печатает распределение по всему корпусу. Не ассертит ничего — нужна,
// чтобы перекалибровать порог по живым числам (go test -run Распределение -v).
func TestMeasure_РаспределениеПоКорпусу(t *testing.T) {
	if os.Getenv("DENSITY_REPORT") == "" {
		t.Skip("диагностика: включается DENSITY_REPORT=1")
	}
	var files []string
	for _, pat := range []string{"labels/*.jpg", "labels/*.png", "_beta_findings/*.jpg"} {
		m, _ := filepath.Glob(filepath.Join(corpusDir, pat))
		files = append(files, m...)
	}
	type row struct {
		name string
		d    Density
	}
	rows := make([]row, 0, len(files))
	for _, p := range files {
		rows = append(rows, row{filepath.Base(p), measureFile(t, p)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].d.FineLoss > rows[j].d.FineLoss })
	for _, r := range rows {
		t.Logf("%-58s edge=%.4f fine_loss=%.3f", r.name, r.d.EdgeFrac, r.d.FineLoss)
	}
}
