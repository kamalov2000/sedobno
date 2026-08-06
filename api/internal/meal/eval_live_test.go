//go:build live

// Тест-харнесс калибровки модуля калорий (feature/calories). НЕ гоняется в CI: тег live
// исключает файл из обычных `go build ./...` / `go test ./...` / `go vet ./...`.
//
// Прогоняет все фото из testdata/meals через РЕАЛЬНЫЙ пайплайн разбора (meal.Pipeline —
// та же валидация, что в проде) для двух моделей (Haiku и Sonnet), пишет сырые результаты
// results_<model>.json и сравнительный отчёт results_report.md. Требует ANTHROPIC_API_KEY.
//
// Запуск:
//
//	set -a; . ./.env; set +a
//	cd api && go test -tags live ./internal/meal/ -run TestMealEval -v -timeout 30m
package meal_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sostav/api/internal/config"
	"github.com/sostav/api/internal/imaging"
	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/meal"
)

// Параметры разбора — те же, что в проде (server/scan.go: ресайз 1500px/JPEG85, один кадр).
const (
	evalMaxDim     = 1500
	evalJPEGQual   = 85
	evalPerImgSecs = 90
)

type modelSpec struct{ name, model string }

type dishOut struct {
	Name       string  `json:"name"`
	Grams      float64 `json:"grams"`
	Kcal       float64 `json:"kcal"`
	ProteinG   float64 `json:"protein_g"`
	FatG       float64 `json:"fat_g"`
	CarbG      float64 `json:"carb_g"`
	Confidence float64 `json:"confidence"`
	// Штучное представление блюда. Без него по отчёту не видно двойного счёта яиц: он
	// проявляется как «база блюда И примитивы про одни и те же яйца», а не в тотале.
	DishKind string          `json:"dish_kind,omitempty"`
	Yolks    int             `json:"yolks,omitempty"`
	Whites   int             `json:"whites,omitempty"`
	Pieces   []llm.MealPiece `json:"pieces,omitempty"`
}

type imgResult struct {
	File       string    `json:"file"`
	Status     string    `json:"status"`
	Dishes     []dishOut `json:"dishes"`
	TotalKcal  float64   `json:"total_kcal"`
	TotalGrams float64   `json:"total_grams"`
	Confidence float64   `json:"confidence"`
	Note       string    `json:"note,omitempty"`
	LatencyMs  int64     `json:"latency_ms"`
	CostRUB    float64   `json:"cost_rub"`
	Err        string    `json:"err,omitempty"`
}

func TestMealEval(t *testing.T) {
	dir := envOr("MEAL_EVAL_DIR", filepath.Join("..", "..", "..", "testdata", "meals"))
	files := listImages(t, dir)
	if len(files) == 0 {
		t.Skipf("нет фото в %s — положите фото еды и запустите с -tags live", dir)
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("нет ANTHROPIC_API_KEY — прогон делает платные vision-вызовы, ключ обязателен")
	}

	gt := parseGroundTruth(filepath.Join(dir, "ground_truth.md"))
	models := parseModels(envOr("MEAL_EVAL_MODELS", "haiku=claude-haiku-4-5,sonnet=claude-sonnet-5"))
	base := baseConfig()

	t.Logf("фото: %d, моделей: %d, ground-truth пометок: %d", len(files), len(models), len(gt))

	results := make(map[string][]imgResult, len(models))
	for _, ms := range models {
		rs := runModel(t, base, ms, dir, files)
		results[ms.name] = rs
		writeJSON(t, filepath.Join(dir, "results_"+ms.name+".json"), rs)
	}

	report := buildReport(models, results, gt)
	reportPath := filepath.Join(dir, "results_report.md")
	if err := os.WriteFile(reportPath, []byte(report), 0o644); err != nil {
		t.Fatalf("запись отчёта: %v", err)
	}
	t.Logf("отчёт готов: %s", reportPath)
}

// runModel прогоняет все фото через пайплайн с указанной vision-моделью.
func runModel(t *testing.T, base config.Config, ms modelSpec, dir string, files []string) []imgResult {
	cfg := base
	cfg.MealVisionModel = ms.model
	analyzer, err := llm.NewMealAnalyzer(&cfg)
	if err != nil {
		t.Fatalf("[%s] сборка провайдера: %v", ms.name, err)
	}
	// Калибровочный харнесс гоняет каждую модель как основную, без эскалации: нам нужен
	// «чистый» разбор каждой модели по отдельности, а не составной пайплайн.
	pipe := meal.New(analyzer, nil, "off", 4, slog.New(slog.NewTextHandler(io.Discard, nil)))

	out := make([]imgResult, 0, len(files))
	for _, f := range files {
		r := runOne(pipe, dir, f)
		out = append(out, r)
		if r.Err != "" {
			t.Logf("[%s] %-28s ОШИБКА: %s", ms.name, f, r.Err)
		} else {
			t.Logf("[%s] %-28s %-14s %d блюд · Σ%.0fг · %.0f ккал · conf %.2f · %dмс · %.3f₽",
				ms.name, f, r.Status, len(r.Dishes), r.TotalGrams, r.TotalKcal, r.Confidence, r.LatencyMs, r.CostRUB)
		}
	}
	return out
}

func runOne(pipe *meal.Pipeline, dir, file string) imgResult {
	res := imgResult{File: file}
	raw, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		res.Err = "чтение файла: " + err.Error()
		return res
	}
	jpg, err := imaging.Normalize(raw, evalMaxDim, evalJPEGQual)
	if err != nil {
		res.Err = "нормализация: " + err.Error()
		return res
	}
	ctx, cancel := context.WithTimeout(context.Background(), evalPerImgSecs*time.Second)
	defer cancel()

	out, perr := pipe.Run(ctx, []llm.Image{{Data: jpg, MIME: "image/jpeg"}})
	res.Status = out.Status
	res.Confidence = out.Confidence
	res.Note = out.Note
	res.LatencyMs = out.LatencyMs
	res.CostRUB = out.CostRUB
	if perr != nil {
		res.Err = perr.Error()
	}
	for _, d := range out.Dishes {
		res.Dishes = append(res.Dishes, dishOut{
			Name: d.Name, Grams: d.Grams, Kcal: d.Kcal, ProteinG: d.ProteinG,
			FatG: d.FatG, CarbG: d.CarbG, Confidence: d.Confidence,
			DishKind: d.DishKind, Yolks: d.Yolks, Whites: d.Whites, Pieces: d.Pieces,
		})
		res.TotalGrams += d.Grams
	}
	res.TotalKcal = out.Total.Kcal
	return res
}

// baseConfig собирает минимальный конфиг под NewMealAnalyzer (без MustLoad — тому нужны
// все обязательные секреты приложения, а харнессу — только доступ к Claude).
func baseConfig() config.Config {
	return config.Config{
		LLMProvider:          "anthropic",
		AnthropicAPIKey:      os.Getenv("ANTHROPIC_API_KEY"),
		AnthropicBaseURL:     envOr("ANTHROPIC_BASE_URL", "https://api.anthropic.com"),
		AnthropicVersion:     envOr("ANTHROPIC_VERSION", "2023-06-01"),
		AnthropicVisionModel: envOr("ANTHROPIC_VISION_MODEL", "claude-haiku-4-5"),
		LLMTimeoutSec:        evalPerImgSecs,
		LLMUSDRUB:            envFloat("LLM_USD_RUB", 95),
	}
}

// --- отчёт ---

func buildReport(models []modelSpec, results map[string][]imgResult, gt map[string]float64) string {
	var b strings.Builder
	b.WriteString("# Отчёт прогона модуля калорий\n\n")
	b.WriteString("Прогон через реальный пайплайн разбора (та же валидация, что в проде).\n\n")

	files := sortedFiles(results, models)

	// Сводная таблица: по каждому фото — статус/Σграммовка/ккал/conf/Δг% каждой модели.
	b.WriteString("## Сводная таблица\n\n")
	b.WriteString("| Фото | GT,г |")
	for _, m := range models {
		fmt.Fprintf(&b, " %s: статус | Σг | ккал | conf | Δг%% |", m.name)
	}
	b.WriteString("\n|---|---|")
	for range models {
		b.WriteString("---|---|---|---|---|")
	}
	b.WriteByte('\n')

	for _, f := range files {
		gtg, hasGT := gt[f]
		gtCell := "—"
		if hasGT {
			gtCell = fmtF(gtg, 0)
		}
		fmt.Fprintf(&b, "| %s | %s |", f, gtCell)
		for _, m := range models {
			r := findRes(results[m.name], f)
			dev := "—"
			if hasGT && gtg > 0 && r != nil && r.Err == "" && r.TotalGrams > 0 {
				dev = fmt.Sprintf("%+.0f", (r.TotalGrams-gtg)/gtg*100)
			}
			if r == nil || r.Err != "" {
				fmt.Fprintf(&b, " ошибка | — | — | — | %s |", dev)
				continue
			}
			fmt.Fprintf(&b, " %s | %s | %s | %.2f | %s |",
				r.Status, fmtF(r.TotalGrams, 0), fmtF(r.TotalKcal, 0), r.Confidence, dev)
		}
		b.WriteByte('\n')
	}

	// Детализация блюд — для качественной оценки (разбивка составных тарелок, названия).
	b.WriteString("\n## Распознанные блюда по фото\n\n")
	for _, f := range files {
		fmt.Fprintf(&b, "### %s", f)
		if g, ok := gt[f]; ok {
			fmt.Fprintf(&b, " (ground truth: %s г)", fmtF(g, 0))
		}
		b.WriteString("\n\n")
		for _, m := range models {
			r := findRes(results[m.name], f)
			if r == nil {
				continue
			}
			fmt.Fprintf(&b, "- **%s** [%s]", m.name, r.Status)
			if r.Err != "" {
				fmt.Fprintf(&b, " — ошибка: %s\n", r.Err)
				continue
			}
			if r.Note != "" {
				fmt.Fprintf(&b, " _%s_", r.Note)
			}
			b.WriteByte('\n')
			for _, d := range r.Dishes {
				fmt.Fprintf(&b, "  - %s — %sг, %s ккал (Б%s Ж%s У%s), conf %.2f\n",
					d.Name, fmtF(d.Grams, 0), fmtF(d.Kcal, 0), fmtF(d.ProteinG, 0), fmtF(d.FatG, 0), fmtF(d.CarbG, 0), d.Confidence)
			}
		}
		b.WriteByte('\n')
	}

	// Итоги по моделям + юнит-экономика.
	b.WriteString("## Итоги по моделям\n\n")
	b.WriteString("| Модель | Фото | ok | not_food | label | error | ср.latency | ср.conf | ср.|Δг%| | Σстоимость | ₽/разбор | ₽/мес (90 разборов) |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, m := range models {
		s := summarize(results[m.name], gt)
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %.0fмс | %.2f | %s | %.2f₽ | %.3f₽ | %.0f₽ |\n",
			m.name, s.count, s.ok, s.notFood, s.label, s.errs, s.avgLatency, s.avgConf,
			s.devCell(), s.totalCost, s.perAnalysis, s.perAnalysis*90)
	}
	b.WriteString("\nΔг% — отклонение суммарной граммовки от ground truth (только фото с известным весом). ")
	b.WriteString("₽/мес — себестоимость при 3 разборах/день × 30 дней. Для сравнения: подписка 249₽/мес (СБП) — ")
	b.WriteString("себестоимость разборов должна оставлять маржу с запасом.\n")

	return b.String()
}

type modelSummary struct {
	count, ok, notFood, label, errs int
	avgLatency, avgConf             float64
	totalCost, perAnalysis          float64
	meanAbsDev                      float64
	devN                            int
}

func (s modelSummary) devCell() string {
	if s.devN == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", s.meanAbsDev)
}

func summarize(rs []imgResult, gt map[string]float64) modelSummary {
	var s modelSummary
	s.count = len(rs)
	var latSum, confSum, devSum float64
	for _, r := range rs {
		s.totalCost += r.CostRUB
		latSum += float64(r.LatencyMs)
		switch {
		case r.Err != "" || r.Status == "error":
			s.errs++
		case r.Status == "ok":
			s.ok++
			confSum += r.Confidence
		case r.Status == "not_food":
			s.notFood++
		case r.Status == "label_detected":
			s.label++
		}
		if g, ok := gt[r.File]; ok && g > 0 && r.Err == "" && r.TotalGrams > 0 {
			devSum += math.Abs((r.TotalGrams - g) / g * 100)
			s.devN++
		}
	}
	if s.count > 0 {
		s.avgLatency = latSum / float64(s.count)
		s.perAnalysis = s.totalCost / float64(s.count)
	}
	if s.ok > 0 {
		s.avgConf = confSum / float64(s.ok)
	}
	if s.devN > 0 {
		s.meanAbsDev = devSum / float64(s.devN)
	}
	return s
}

// --- утилиты ---

var groundTruthLine = regexp.MustCompile(`(?i)^\s*([^\s|]+\.(?:jpe?g|png|webp))\s*\|\s*(\d+(?:\.\d+)?)`)

func parseGroundTruth(path string) map[string]float64 {
	out := map[string]float64{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		m := groundTruthLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if v, err := strconv.ParseFloat(m[2], 64); err == nil {
			out[m[1]] = v
		}
	}
	return out
}

func parseModels(raw string) []modelSpec {
	var out []modelSpec
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, model, ok := strings.Cut(part, "=")
		if !ok {
			name, model = model, name // «model» без имени — имя = модель
			name = model
		}
		out = append(out, modelSpec{name: strings.TrimSpace(name), model: strings.TrimSpace(model)})
	}
	return out
}

var imageExt = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}

func listImages(t *testing.T, dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("папка %s недоступна: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if imageExt[strings.ToLower(filepath.Ext(e.Name()))] {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func sortedFiles(results map[string][]imgResult, models []modelSpec) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range models {
		for _, r := range results[m.name] {
			if !seen[r.File] {
				seen[r.File] = true
				out = append(out, r.File)
			}
		}
	}
	sort.Strings(out)
	return out
}

func findRes(rs []imgResult, file string) *imgResult {
	for i := range rs {
		if rs[i].File == file {
			return &rs[i]
		}
	}
	return nil
}

func writeJSON(t *testing.T, path string, v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("сериализация %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("запись %s: %v", path, err)
	}
}

func fmtF(v float64, prec int) string {
	return strconv.FormatFloat(v, 'f', prec, 64)
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}
