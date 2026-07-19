package main

// Смоук на ЖИВОМ API (Этап 4). Не нагрузочный тест: объём намеренно мал, задача —
// увидеть поведение на настоящих ответах Anthropic, а не найти потолок.
//
// Что меряем и почему именно так:
//   - латентность — по HTTP, как её видит пользователь;
//   - провайдер и стоимость — из БД (scans/meals пишут provider, latency_ms,
//     cost_estimate), а не из ответа: клиенту эти поля не отдаются, и это правильно;
//   - доля эскалаций — по provider='anthropic-fallback' в scans. У сканера эскалация
//     реальна (Haiku→Sonnet при неуверенном чтении), у модуля калорий её нет вовсе:
//     meal.Pipeline делает ровно один vision-вызов. Поэтому доля считается только
//     для сканов, и на разборах еды она не печатается — не потому что 0%, а потому
//     что механизма нет.
//
// Тратит РЕАЛЬНЫЕ деньги, поэтому требует REAL_API_ACK=1.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// realOutcome — исход одного живого вызова с диагностикой не-200.
type realOutcome struct {
	file    string
	status  int
	kind    string
	latency time.Duration
	// headers — заголовки не-200 ответа: на живых вызовах именно они объясняют
	// поведение ретраев (retry-after, request-id для обращения в поддержку).
	headers string
}

// dbCall — фактические цифры вызова, поднятые из БД после прогона.
type dbCall struct {
	provider  string
	latencyMs int64
	costRUB   float64
	status    string
}

// confirmRealSpend печатает предупреждение и требует явного подтверждения.
// Гейт намеренно грубый: переменная окружения, а не флаг, — случайно
// протащить её в скрипт сложнее, чем дописать флаг в командную строку.
func confirmRealSpend(calls int, rubEstimate float64) {
	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║  ВНИМАНИЕ: прогон делает ПЛАТНЫЕ вызовы Anthropic API        ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	fmt.Printf("  Запланировано вызовов: ~%d\n", calls)
	fmt.Printf("  Оценка расхода:        ~%.0f ₽ (верхняя граница)\n", rubEstimate)
	fmt.Println("  Ключ берётся из ANTHROPIC_API_KEY окружения.")
	fmt.Println()
	if os.Getenv("REAL_API_ACK") != "1" {
		fatal("не подтверждено. Для запуска: REAL_API_ACK=1 make load-test-real")
	}
	fmt.Println("  REAL_API_ACK=1 — подтверждено, начинаем.")
	fmt.Println()
}

// loadPhotos читает изображения из папки (без рекурсии), сортируя по имени
// для воспроизводимости прогона.
func loadPhotos(dir string) ([]struct {
	name string
	data []byte
}, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []struct {
		name string
		data []byte
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".jpg", ".jpeg", ".png", ".webp":
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		b, rerr := os.ReadFile(filepath.Join(dir, n))
		if rerr != nil {
			return nil, rerr
		}
		out = append(out, struct {
			name string
			data []byte
		}{n, b})
	}
	return out, nil
}

// postPhoto шлёт одно фото на указанный эндпоинт и сохраняет заголовки не-200.
func (r *runner) postPhoto(ctx context.Context, token, endpoint, name string, photo []byte) realOutcome {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("photo", name)
	if err != nil {
		return realOutcome{file: name, kind: "transport"}
	}
	_, _ = part.Write(photo)
	_ = mw.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.apiBase+endpoint, &body)
	if err != nil {
		return realOutcome{file: name, kind: "transport"}
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)

	start := time.Now()
	resp, err := loadClient.Do(req)
	lat := time.Since(start)
	if err != nil {
		return realOutcome{file: name, kind: "transport", latency: lat}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	o := realOutcome{file: name, status: resp.StatusCode, latency: lat, kind: classify(resp.StatusCode, raw)}
	if resp.StatusCode != http.StatusOK {
		// Логируем ровно те заголовки, которые объясняют поведение ретраев.
		var b strings.Builder
		for _, h := range []string{"Retry-After", "Request-Id", "X-Should-Retry", "Anthropic-Ratelimit-Requests-Remaining"} {
			if v := resp.Header.Get(h); v != "" {
				fmt.Fprintf(&b, "%s=%s ", h, v)
			}
		}
		o.headers = strings.TrimSpace(b.String())
	}
	return o
}

// scenarioReal — живой смоук: clients параллельных клиентов, каждый шлёт perClient
// фото по кругу из набора. endpoint выбирает пайплайн (/api/meals или /api/scan).
func (r *runner) scenarioReal(ctx context.Context, us []testUser, photoDir, endpoint string, clients, perClient int) {
	photos, err := loadPhotos(photoDir)
	if err != nil || len(photos) == 0 {
		fatal("не удалось прочитать фото из %s: %v", photoDir, err)
	}
	if clients > len(us) {
		fatal("клиентов (%d) больше, чем засеяно пользователей (%d)", clients, len(us))
	}
	pick := us[:clients]
	if terr := r.issueTokens(pick); terr != nil {
		fatal("выпуск токенов: %v", terr)
	}

	fmt.Printf("=== Живой смоук: %s, %d клиентов × %d вызовов, %d фото в наборе ===\n",
		endpoint, clients, perClient, len(photos))
	for _, p := range photos {
		fmt.Printf("    %s (%d КБ)\n", p.name, len(p.data)/1024)
	}
	fmt.Println()

	since := time.Now()
	var (
		mu      sync.Mutex
		results []realOutcome
		wg      sync.WaitGroup
	)
	start := time.Now()
	for i := range pick {
		wg.Add(1)
		go func(idx int, u testUser) {
			defer wg.Done()
			for j := 0; j < perClient; j++ {
				p := photos[(idx*perClient+j)%len(photos)]
				o := r.postPhoto(ctx, u.token, endpoint, p.name, p.data)
				mu.Lock()
				results = append(results, o)
				mu.Unlock()
				time.Sleep(time.Duration(500+idx*40) * time.Millisecond)
			}
		}(i, pick[i])
	}
	wg.Wait()
	elapsed := time.Since(start)

	r.reportReal(ctx, endpoint, results, pick, since, elapsed)
}

// reportReal печатает клиентскую картину и сверяет её с фактическими записями в БД.
func (r *runner) reportReal(ctx context.Context, endpoint string, results []realOutcome,
	us []testUser, since time.Time, elapsed time.Duration) {

	fmt.Printf("\n--- Клиентская картина (%s) ---\n", endpoint)
	lat := make([]time.Duration, 0, len(results))
	byKind := map[string]int{}
	for _, o := range results {
		byKind[o.kind]++
		if o.kind != "transport" {
			lat = append(lat, o.latency)
		}
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	if len(lat) > 0 {
		fmt.Printf("Вызовов: %d за %s\n", len(results), elapsed.Round(time.Second))
		fmt.Printf("Латентность: min=%s p50=%s p95=%s max=%s\n",
			lat[0].Round(time.Millisecond), pct(lat, 50).Round(time.Millisecond),
			pct(lat, 95).Round(time.Millisecond), lat[len(lat)-1].Round(time.Millisecond))
	}
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	fmt.Println("Исходы:")
	for _, k := range kinds {
		fmt.Printf("  %-22s %d\n", k, byKind[k])
	}
	// Не-200 с заголовками — материал для разбора ретраев на живых ответах.
	shown := false
	for _, o := range results {
		if o.status != 0 && o.status != 200 {
			if !shown {
				fmt.Println("Не-200 ответы (заголовки):")
				shown = true
			}
			fmt.Printf("  %s → %d %s [%s]\n", o.file, o.status, o.kind, o.headers)
		}
	}
	if !shown {
		fmt.Println("Не-200 ответов не было.")
	}

	calls, err := r.dbCalls(ctx, endpoint, us, since)
	if err != nil {
		fmt.Printf("\nне удалось поднять записи из БД: %v\n", err)
		return
	}
	if len(calls) == 0 {
		fmt.Println("\nв БД нет записей за прогон (все исходы не-ok?)")
		return
	}

	fmt.Printf("\n--- Факт из БД (%d записей) ---\n", len(calls))
	var totalCost float64
	byProvider := map[string]int{}
	provLat := map[string][]int64{}
	for _, c := range calls {
		totalCost += c.costRUB
		byProvider[c.provider]++
		provLat[c.provider] = append(provLat[c.provider], c.latencyMs)
	}
	provs := make([]string, 0, len(byProvider))
	for p := range byProvider {
		provs = append(provs, p)
	}
	sort.Strings(provs)
	for _, p := range provs {
		ls := provLat[p]
		sort.Slice(ls, func(i, j int) bool { return ls[i] < ls[j] })
		fmt.Printf("  %-22s вызовов=%2d  латентность min=%dмс med=%dмс max=%dмс\n",
			p, byProvider[p], ls[0], ls[len(ls)/2], ls[len(ls)-1])
	}
	fmt.Printf("Стоимость: сумма=%.4f ₽, средняя=%.4f ₽/вызов\n", totalCost, totalCost/float64(len(calls)))

	// Доля эскалаций — только для сканера: у калорий механизма нет (см. шапку файла).
	if strings.Contains(endpoint, "scan") {
		esc := 0
		for _, c := range calls {
			if strings.Contains(c.provider, "fallback") {
				esc++
			}
		}
		fmt.Printf("ЭСКАЛАЦИИ на Sonnet: %d из %d (%.0f%%)\n",
			esc, len(calls), 100*float64(esc)/float64(len(calls)))
	} else {
		fmt.Println("Доля эскалаций: неприменимо — у модуля калорий один vision-вызов без fallback.")
	}
}

// dbCalls поднимает записи прогона из scans или meals.
func (r *runner) dbCalls(ctx context.Context, endpoint string, us []testUser, since time.Time) ([]dbCall, error) {
	ids := make([]int64, len(us))
	for i, u := range us {
		ids[i] = u.id
	}
	q := `SELECT COALESCE(provider,''), COALESCE(latency_ms,0), COALESCE(cost_estimate,0), status
	        FROM meals WHERE user_id = ANY($1) AND created_at >= $2`
	if strings.Contains(endpoint, "scan") {
		q = `SELECT COALESCE(provider,''), COALESCE(latency_ms,0), COALESCE(cost_estimate,0), status
		       FROM scans WHERE user_id = ANY($1) AND created_at >= $2`
	}
	rows, err := r.pool.Query(ctx, q, ids, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dbCall
	for rows.Next() {
		var c dbCall
		if serr := rows.Scan(&c.provider, &c.latencyMs, &c.costRUB, &c.status); serr != nil {
			return nil, serr
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
