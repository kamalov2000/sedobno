// Команда loadtest — нагрузочный стенд модуля калорий (локально, LLM_PROVIDER=mock).
//
// Почему свой драйвер на Go, а не k6/vegeta: клиентам нужны ВАЛИДНЫЕ JWT, а токены
// подписывает internal/auth. Из внешнего инструмента это означало бы либо повторить
// HMAC на JS, либо городить вспомогательный генератор — обе копии со временем
// разъезжаются с сервером. Здесь токены выпускаются тем же кодом, что их проверяет.
//
// Сценарии (флаг -scenario):
//
//	a — «ужин»: N параллельных клиентов, по 3 разбора с паузами, держим -duration.
//	b — «гонка квоты»: один пользователь, 20 параллельных запросов при квоте 10.
//	    Проверяем, что списано РОВНО 10 — и по ответам, и по счётчику в БД.
//	c — «шторм ошибок»: то же, что a, но сервер поднят с MOCK_LLM_ERROR_RATE>0.
//	    Проверяем, что неуспешные попытки не списывают квоту.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"os"
	"sort"
	"sync"

	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sostav/api/internal/auth"
)

func main() {
	var (
		apiBase   = flag.String("api", envOr("LOAD_API_BASE", "http://localhost:8080"), "базовый URL API")
		dbURL     = flag.String("db", os.Getenv("DATABASE_URL"), "DSN Postgres для сида и проверки счётчиков")
		secret    = flag.String("secret", os.Getenv("JWT_SECRET"), "тот же JWT_SECRET, что у сервера")
		scenario  = flag.String("scenario", "a", "сценарий: a | b | c")
		clients   = flag.Int("clients", 50, "параллельных клиентов (сценарий a/c)")
		perClient = flag.Int("requests", 3, "запросов на клиента (сценарий a/c)")
		duration  = flag.Duration("duration", 2*time.Minute, "минимальная длительность прогона (a/c)")
		users     = flag.Int("users", 100, "сколько тестовых пользователей засеять")
		photoKB   = flag.Int("photo-kb", 100, "целевой размер фикстурного фото, КБ")
		racePar   = flag.Int("race-parallel", 20, "параллельных запросов в сценарии b")
		racePre   = flag.Int("race-preset-used", 0, "стартовое free_meals_used в сценарии b")
	)
	flag.Parse()

	if *secret == "" || *dbURL == "" {
		fatal("нужны -secret (JWT_SECRET) и -db (DATABASE_URL)")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *dbURL)
	if err != nil {
		fatal("подключение к БД: %v", err)
	}
	defer pool.Close()

	photo := makeJPEG(*photoKB)
	fmt.Printf("фикстурное фото: %d КБ\n", len(photo)/1024)

	seeded, err := seedUsers(ctx, pool, *users)
	if err != nil {
		fatal("сид пользователей: %v", err)
	}
	fmt.Printf("засеяно тестовых пользователей: %d\n\n", len(seeded))

	rt := &runner{apiBase: *apiBase, secret: *secret, photo: photo, pool: pool}

	switch *scenario {
	case "a", "c":
		rt.scenarioLoad(ctx, seeded, *clients, *perClient, *duration, *scenario)
	case "b":
		rt.scenarioQuotaRace(ctx, seeded[0], *racePar, *racePre)
	default:
		fatal("неизвестный сценарий %q (a|b|c)", *scenario)
	}
}

// --- подготовка ---

type testUser struct {
	id    int64
	tgID  int64
	token string
}

// seedUsers создаёт пользователей напрямую в БД и обнуляет их счётчики, чтобы прогон
// был воспроизводим (повторный запуск не упирается в квоту от прошлого раза).
// telegram_id берутся из заведомо тестового диапазона, чтобы не пересечься с живыми.
const testTGBase = 900_000_000_000

func seedUsers(ctx context.Context, pool *pgxpool.Pool, n int) ([]testUser, error) {
	out := make([]testUser, 0, n)
	for i := 0; i < n; i++ {
		tgID := int64(testTGBase + i)
		var id int64
		err := pool.QueryRow(ctx, `
			INSERT INTO users (telegram_id, username, free_scans_used, free_meals_used, is_subscribed)
			VALUES ($1, $2, 0, 0, false)
			ON CONFLICT (telegram_id) DO UPDATE
			   SET free_scans_used = 0, free_meals_used = 0, is_subscribed = false
			RETURNING id`, tgID, fmt.Sprintf("loadtest_%d", i)).Scan(&id)
		if err != nil {
			return nil, err
		}
		out = append(out, testUser{id: id, tgID: tgID})
	}
	return out, nil
}

// issueTokens выпускает JWT тем же кодом, что использует сервер.
func (r *runner) issueTokens(us []testUser) error {
	for i := range us {
		// TTL с запасом на длительность прогона: протухший токен дал бы 401 и
		// исказил картину ошибок.
		t, err := auth.Issue(r.secret, us[i].id, us[i].tgID, time.Hour)
		if err != nil {
			return err
		}
		us[i].token = t
	}
	return nil
}

// makeJPEG рисует шумное изображение и кодирует в JPEG примерно нужного размера.
// Шум важен: гладкая заливка сжимается в единицы КБ, и стенд не грузил бы ни сеть,
// ни декодер imaging.Normalize — то есть мерил бы не то.
func makeJPEG(targetKB int) []byte {
	side := 400
	for {
		img := image.NewRGBA(image.Rect(0, 0, side, side*3/4))
		rnd := rand.NewChaCha8([32]byte{})
		buf := make([]byte, 4)
		for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
			for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
				rnd.Read(buf)
				img.Set(x, y, color.RGBA{buf[0], buf[1], buf[2], 255})
			}
		}
		var b bytes.Buffer
		if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 85}); err != nil {
			fatal("кодирование фикстуры: %v", err)
		}
		if b.Len() >= targetKB*1024 || side >= 4000 {
			return b.Bytes()
		}
		side = side * 3 / 2
	}
}

// --- HTTP-клиент стенда ---

type runner struct {
	apiBase string
	secret  string
	photo   []byte
	pool    *pgxpool.Pool
}

// outcome — классифицированный исход одного запроса.
type outcome struct {
	kind    string // ok | quota | busy | rate_limited | server_error | transport
	status  int
	latency time.Duration
}

// postMeal шлёт один разбор приёма пищи и классифицирует ответ.
func (r *runner) postMeal(ctx context.Context, token string) outcome {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("photo", "meal.jpg")
	if err != nil {
		return outcome{kind: "transport"}
	}
	_, _ = part.Write(r.photo)
	_ = mw.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.apiBase+"/api/meals", &body)
	if err != nil {
		return outcome{kind: "transport"}
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)

	start := time.Now()
	resp, err := loadClient.Do(req)
	lat := time.Since(start)
	if err != nil {
		return outcome{kind: "transport", latency: lat}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	return outcome{kind: classify(resp.StatusCode, raw), status: resp.StatusCode, latency: lat}
}

// classify раскладывает ответ по видам исходов. Разделять 402 (квота), 429 (шлюз
// перегрузки либо рейт-лимит) и 5xx критично: только 5xx — настоящий отказ сервиса,
// остальное — штатное поведение под нагрузкой.
func classify(status int, body []byte) string {
	switch {
	case status == http.StatusOK:
		var r struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(body, &r)
		if r.Status == "ok" {
			return "ok"
		}
		return "non_ok:" + r.Status
	case status == http.StatusPaymentRequired:
		return "quota"
	case status == http.StatusTooManyRequests:
		var r struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &r)
		if r.Error == "rate_limited" {
			return "rate_limited"
		}
		return "busy"
	case status >= 500:
		return "server_error"
	default:
		return fmt.Sprintf("http_%d", status)
	}
}

// loadClient — общий клиент стенда. Таймаут заведомо больше ожидаемой латентности
// (mock-задержка + ретрай), иначе стенд бил бы по своим же таймаутам и рисовал
// несуществующие сбои. Пул соединений широкий — узким местом должен быть сервер.
var loadClient = &http.Client{
	Timeout: 120 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 200,
		IdleConnTimeout:     90 * time.Second,
	},
}

// --- сценарии ---

// scenarioLoad — сценарии A и C: clients параллельных клиентов, каждый делает
// perClient разборов с паузами; прогон длится не меньше duration.
func (r *runner) scenarioLoad(ctx context.Context, us []testUser, clients, perClient int, duration time.Duration, name string) {
	if clients > len(us) {
		fatal("клиентов (%d) больше, чем засеяно пользователей (%d)", clients, len(us))
	}
	pick := us[:clients]
	if err := r.issueTokens(pick); err != nil {
		fatal("выпуск токенов: %v", err)
	}

	fmt.Printf("=== Сценарий %s: %d клиентов × %d запросов, минимум %s ===\n",
		name, clients, perClient, duration)
	before, _ := r.quotaSum(ctx, pick)

	var (
		mu       sync.Mutex
		results  []outcome
		deadline = time.Now().Add(duration)
		wg       sync.WaitGroup
	)
	start := time.Now()
	for i := range pick {
		wg.Add(1)
		go func(u testUser) {
			defer wg.Done()
			// Клиенты стартуют вразнобой: одновременный залп 50 запросов — не «ужин»,
			// а искусственный пик, которого в жизни не бывает.
			time.Sleep(time.Duration(rand.IntN(2000)) * time.Millisecond)
			// Потолок запросов на клиента держим НИЖЕ бесплатной квоты (10): иначе
			// прогон упрётся в paywall и вместо латентности мы измерим 402. Квоту
			// проверяет сценарий B, здесь она только мешала бы.
			for sent := 0; sent < perClient && time.Now().Before(deadline); sent++ {
				o := r.postMeal(ctx, u.token)
				mu.Lock()
				results = append(results, o)
				mu.Unlock()
				// Пауза между приёмами: человек не шлёт фото подряд, плюс это держит
				// темп в рамках боевого рейт-лимита 5/мин.
				time.Sleep(time.Duration(9000+rand.IntN(4000)) * time.Millisecond)
			}
		}(pick[i])
	}
	wg.Wait()
	elapsed := time.Since(start)

	after, _ := r.quotaSum(ctx, pick)
	report(name, results, elapsed)
	fmt.Printf("Списано квоты за прогон: %d (было %d, стало %d)\n", after-before, before, after)

	okCount := 0
	for _, o := range results {
		if o.kind == "ok" {
			okCount++
		}
	}
	fmt.Printf("Успешных разборов: %d\n", okCount)
	if after-before != okCount {
		fmt.Printf("!! РАСХОЖДЕНИЕ: списано %d при %d успешных — ложное списание\n", after-before, okCount)
	} else {
		fmt.Printf("OK: списание ровно по успешным разборам, ложных списаний нет\n")
	}
	r.printPoolStats(ctx)
}

// scenarioQuotaRace — сценарий B: 20 параллельных запросов одним пользователем при
// квоте 10. Атомарность списания проверяется тремя независимыми числами: ответы с
// ok, ответы с отказом по квоте и счётчик free_meals_used в БД.
func (r *runner) scenarioQuotaRace(ctx context.Context, u testUser, parallel, presetUsed int) {
	const quota = 10

	one := []testUser{u}
	if err := r.issueTokens(one); err != nil {
		fatal("выпуск токенов: %v", err)
	}
	u = one[0]

	fmt.Printf("=== Сценарий B: %d параллельных запросов, квота %d, стартовый счётчик %d ===\n",
		parallel, quota, presetUsed)
	// Стартовый счётчик задаётся явно: утечка возникает только когда в полёте больше
	// запросов, чем осталось квоты, и худший случай — счётчик у самого потолка.
	if _, err := r.pool.Exec(ctx,
		`UPDATE users SET free_meals_used = $2 WHERE id = $1`, u.id, presetUsed); err != nil {
		fatal("установка счётчика: %v", err)
	}

	var (
		mu      sync.Mutex
		results []outcome
		wg      sync.WaitGroup
		ready   = make(chan struct{})
	)
	start := time.Now()
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ready // залп строго одновременный — именно он ловит гонку
			o := r.postMeal(ctx, u.token)
			mu.Lock()
			results = append(results, o)
			mu.Unlock()
		}()
	}
	close(ready)
	wg.Wait()
	elapsed := time.Since(start)

	var okN, quotaN, otherN int
	for _, o := range results {
		switch o.kind {
		case "ok":
			okN++
		case "quota":
			quotaN++
		default:
			otherN++
		}
	}

	var used int
	if err := r.pool.QueryRow(ctx,
		`SELECT free_meals_used FROM users WHERE id = $1`, u.id).Scan(&used); err != nil {
		fatal("чтение счётчика: %v", err)
	}

	report("B", results, elapsed)
	fmt.Printf("Успешных: %d | отказов по квоте: %d | прочих: %d\n", okN, quotaN, otherN)
	fmt.Printf("Счётчик free_meals_used в БД: %d (был %d)\n", used, presetUsed)

	// Два РАЗНЫХ свойства, их важно не смешивать:
	//  1) атомарность — счётчик не имеет права превысить квоту ни при какой гонке;
	//  2) утечка выдачи — сколько разборов пользователь получил сверх списанного.
	//     Хендлер осознанно отдаёт результат и когда списание не прошло
	//     (см. комментарий «гонка увела за лимит» в meals.go): работа уже сделана
	//     и оплачена бизнесом. Здесь мы это не судим, а измеряем.
	charged := used - presetUsed
	leak := okN - charged

	if used > quota {
		fmt.Printf("!! БАГ АТОМАРНОСТИ: счётчик %d превысил квоту %d\n", used, quota)
	} else {
		fmt.Printf("OK (атомарность): счётчик %d не превысил квоту %d\n", used, quota)
	}
	fmt.Printf("Списано: %d | выдано разборов: %d | сверх списанного: %d\n", charged, okN, leak)
	if leak > 0 {
		fmt.Printf("!! УТЕЧКА ВЫДАЧИ: %d разборов отданы бесплатно сверх квоты\n", leak)
	} else {
		fmt.Println("OK (выдача): бесплатных разборов сверх квоты не выдано")
	}
}

// quotaSum — суммарно списанная квота по набору пользователей (для проверки
// «ложных списаний»: разница до/после должна равняться числу успешных разборов).
func (r *runner) quotaSum(ctx context.Context, us []testUser) (int, error) {
	ids := make([]int64, len(us))
	for i, u := range us {
		ids[i] = u.id
	}
	var sum int
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(free_meals_used), 0) FROM users WHERE id = ANY($1)`, ids).Scan(&sum)
	return sum, err
}

// --- отчёт ---

func report(name string, results []outcome, elapsed time.Duration) {
	if len(results) == 0 {
		fmt.Println("нет результатов")
		return
	}
	byKind := map[string]int{}
	lat := make([]time.Duration, 0, len(results))
	for _, o := range results {
		byKind[o.kind]++
		// В латентность берём только доехавшие ответы: транспортные обрывы
		// исказили бы перцентили временем до разрыва, а не временем обработки.
		if o.kind != "transport" {
			lat = append(lat, o.latency)
		}
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })

	fmt.Printf("\n--- Итоги сценария %s ---\n", name)
	fmt.Printf("Запросов: %d за %s (%.1f rps)\n", len(results), elapsed.Round(time.Second),
		float64(len(results))/elapsed.Seconds())
	fmt.Printf("Латентность: p50=%s p95=%s p99=%s min=%s max=%s\n",
		pct(lat, 50).Round(time.Millisecond), pct(lat, 95).Round(time.Millisecond),
		pct(lat, 99).Round(time.Millisecond), lat[0].Round(time.Millisecond),
		lat[len(lat)-1].Round(time.Millisecond))
	fmt.Println("Исходы:")
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		fmt.Printf("  %-16s %d\n", k, byKind[k])
	}
	// В сценарии C провайдер намеренно сбоит, и часть 502 — ожидаемый исход
	// (обе попытки не прошли). Порог «ноль 5xx» относится только к сценарию A,
	// где провайдер исправен.
	if n := byKind["server_error"]; n > 0 {
		if name == "c" {
			fmt.Printf("   (5xx: %d — ожидаемо при MOCK_LLM_ERROR_RATE>0: обе попытки не прошли)\n", n)
		} else {
			fmt.Printf("!! 5xx: %d — порог прохождения требует нуля\n", n)
		}
	}
}

func pct(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := (len(sorted)*p + 99) / 100
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}

// printPoolStats читает расширенный /health (DEBUG_STATS=1) и печатает статистику пула.
func (r *runner) printPoolStats(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.apiBase+"/api/health", nil)
	if err != nil {
		return
	}
	resp, err := loadClient.Do(req)
	if err != nil {
		fmt.Printf("статистика пула недоступна: %v\n", err)
		return
	}
	defer resp.Body.Close()
	var h struct {
		Pool *struct {
			MaxConns          int32  `json:"max_conns"`
			TotalConns        int32  `json:"total_conns"`
			AcquiredMax       int32  `json:"acquired_max"`
			AcquireCount      int64  `json:"acquire_count"`
			EmptyAcquireCount int64  `json:"empty_acquire_count"`
			AcquireDurationMs int64  `json:"acquire_duration_total_ms"`
			Note              string `json:"note"`
		} `json:"pool"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil || h.Pool == nil {
		fmt.Println("статистика пула не отдана (нужен DEBUG_STATS=1)")
		return
	}
	p := h.Pool
	fmt.Printf("\nПул pgxpool: max_conns=%d total=%d пик_занятых=%d\n", p.MaxConns, p.TotalConns, p.AcquiredMax)
	fmt.Printf("  acquire_count=%d empty_acquire_count=%d ожидание_всего=%dмс\n",
		p.AcquireCount, p.EmptyAcquireCount, p.AcquireDurationMs)
	// Насыщение — это пик, упёршийся в потолок. empty_acquire_count печатаем как
	// справочное: в него попадает и прогрев пула на первых запросах.
	switch {
	case p.AcquiredMax >= p.MaxConns:
		fmt.Printf("  !! %s\n", p.Note)
	default:
		fmt.Printf("  OK: пик %d из %d — пул не был узким местом\n", p.AcquiredMax, p.MaxConns)
	}
}

// --- мелочи ---

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "loadtest: "+format+"\n", args...)
	os.Exit(1)
}
