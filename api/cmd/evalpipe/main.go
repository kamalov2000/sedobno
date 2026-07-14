// Команда evalpipe прогоняет папку фото этикеток через полный пайплайн (§4) и
// печатает таблицу: файл → извлечено → сматчено → score → стоимость → латентность,
// плюс сводный отчёт (§12 SPEC). Нужна для выбора провайдера, контроля качества
// промптов и оценки стоимости скана.
//
// Провайдер берётся из LLM_PROVIDER. В режиме mock обрабатываются .txt-фикстуры
// (каждая строка — ингредиент), что позволяет прогнать пайплайн без ключей и денег.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	appconfig "github.com/sostav/api/config"
	seeddata "github.com/sostav/api/data"
	"github.com/sostav/api/internal/config"
	"github.com/sostav/api/internal/dict"
	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/logging"
	"github.com/sostav/api/internal/pipeline"
	"github.com/sostav/api/internal/scoring"
)

func main() {
	dir := flag.String("dir", "./testdata/labels", "папка с фото этикеток (или .txt-фикстурами в режиме mock)")
	delay := flag.Duration("delay", 0, "пауза между файлами (напр. 30s) — для обхода rate-limit провайдера на free-tier")
	raw := flag.Bool("raw", false, "печатать извлечённый состав (сырые ингредиенты) по каждому кейсу — проверка склейки мультифото")
	flag.Parse()

	cfg := config.MustLoad()
	log := logging.New(cfg.LogLevel)

	items, err := dict.LoadSeed(seeddata.AdditivesJSON)
	must("загрузка словаря", err)
	scorer, err := scoring.Load(appconfig.ScoringYAML)
	must("загрузка scoring.yaml", err)
	provider, err := llm.New(cfg)
	must("инициализация LLM-провайдера", err)

	pipe := pipeline.New(provider, items, scorer, cfg.LLMMaxConcurrent, log)
	pipe.SetExtractFallback(llm.NewExtractFallback(cfg), cfg.ExtractConfidenceMin)

	// Кейсы: позиционные аргументы — мультифото-кейсы (файлы через запятую = один скан);
	// без аргументов — каждый файл из -dir как одиночный кейс (обратная совместимость).
	cases, err := buildCases(flag.Args(), *dir, cfg.LLMProvider == "mock")
	must("сбор кейсов", err)
	if len(cases) == 0 {
		fmt.Printf("evalpipe: кейсов не найдено (папка %s или аргументы).\n", *dir)
		if cfg.LLMProvider == "mock" {
			fmt.Println("evalpipe: в режиме mock ожидаются .txt-файлы (строки = ингредиенты).")
		} else {
			fmt.Println("evalpipe: ожидаются .jpg/.jpeg/.png/.webp. Мультифото: evalpipe a.jpg,b.jpg,c.jpg")
		}
		return
	}

	fmt.Printf("evalpipe: провайдер=%s, кейсов=%d\n\n", provider.Name(), len(cases))

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ФАЙЛ\tСТАТУС\tИЗВЛЕЧ.\tСМАТЧ.\tSCORE\tФЛАГИ\t₽/скан\tТОКЕНЫ\tЛАТЕНТ.")

	var totCost float64
	var totLatency, totTokens int64
	var okCount int

	for i, grp := range cases {
		if i > 0 && *delay > 0 {
			time.Sleep(*delay)
		}
		imgs, name, rerr := readCase(grp)
		if rerr != nil {
			fmt.Fprintf(tw, "%s\tОШИБКА ЧТЕНИЯ\t-\t-\t-\t-\t-\t-\t-\n", name)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		res, err := pipe.Run(ctx, imgs)
		cancel()

		if err != nil {
			fmt.Fprintf(tw, "%s\tERROR: %s\t-\t-\t-\t-\t-\t-\t-\n", name, short(err.Error()))
			continue
		}

		matched := countMatched(res.Matched)
		flags := topFlagTitles(res.Flags)
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%s\t%.4f\t%d\t%dms\n",
			name, res.Status, len(res.IngredientsRaw), matched, res.Score, flags,
			res.CostRUB, res.Usage.InputTokens+res.Usage.OutputTokens, res.LatencyMs)

		totCost += res.CostRUB
		totLatency += res.LatencyMs
		totTokens += int64(res.Usage.InputTokens + res.Usage.OutputTokens)
		if res.Status == pipeline.StatusOK {
			okCount++
		}
		if *raw {
			fmt.Fprintf(tw, "  └─ состав[%d]: %s\t\t\t\t\t\t\t\t\n", len(res.IngredientsRaw), strings.Join(res.IngredientsRaw, " | "))
		}
	}
	tw.Flush()

	n := int64(len(cases))
	fmt.Printf("\nИТОГО: кейсов=%d, ok=%d\n", len(cases), okCount)
	fmt.Printf("Стоимость: сумма=%.4f ₽, средняя=%.4f ₽/скан (цель < 1 ₽)\n", totCost, avgF(totCost, len(cases)))
	fmt.Printf("Латентность: средняя=%d ms\n", avgI(totLatency, n))
	fmt.Printf("Токены: сумма=%d, средние=%d/скан\n", totTokens, avgI(totTokens, n))
}

// buildCases собирает список кейсов. Позиционные аргументы — мультифото-кейсы:
// файлы через запятую («a.jpg,b.jpg») складываются в один скан. Без аргументов —
// каждый файл из dir становится одиночным кейсом (прежнее поведение).
func buildCases(args []string, dir string, mock bool) ([][]string, error) {
	if len(args) > 0 {
		var cases [][]string
		for _, a := range args {
			var grp []string
			for _, p := range strings.Split(a, ",") {
				if p = strings.TrimSpace(p); p != "" {
					grp = append(grp, p)
				}
			}
			if len(grp) > 0 {
				cases = append(cases, grp)
			}
		}
		return cases, nil
	}
	files, err := listFiles(dir, mock)
	if err != nil {
		return nil, err
	}
	cases := make([][]string, 0, len(files))
	for _, f := range files {
		cases = append(cases, []string{f})
	}
	return cases, nil
}

// readCase читает все фото кейса в []llm.Image и формирует его имя (basename'ы через +).
func readCase(paths []string) ([]llm.Image, string, error) {
	imgs := make([]llm.Image, 0, len(paths))
	names := make([]string, 0, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, filepath.Base(p), err
		}
		imgs = append(imgs, llm.Image{Data: data, MIME: mimeOf(p)})
		names = append(names, filepath.Base(p))
	}
	return imgs, strings.Join(names, "+"), nil
}

func listFiles(dir string, mock bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		ok := false
		if mock {
			ok = ext == ".txt"
		} else {
			switch ext {
			case ".jpg", ".jpeg", ".png", ".webp":
				ok = true
			}
		}
		if ok {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

func mimeOf(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".txt":
		return "text/plain"
	default:
		return "image/jpeg"
	}
}

func countMatched(m []pipeline.MatchedIngredient) int {
	n := 0
	for _, x := range m {
		if x.Matched {
			n++
		}
	}
	return n
}

func topFlagTitles(flags []scoring.Flag) string {
	if len(flags) == 0 {
		return "—"
	}
	names := make([]string, 0, len(flags))
	for _, f := range flags {
		names = append(names, fmt.Sprintf("%s(-%d)", f.Title, f.Penalty))
	}
	return strings.Join(names, ", ")
}

func short(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

func avgF(sum float64, n int) float64 {
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func avgI(sum, n int64) int64 {
	if n == 0 {
		return 0
	}
	return sum / n
}

func must(what string, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "evalpipe: %s: %v\n", what, err)
		os.Exit(1)
	}
}
