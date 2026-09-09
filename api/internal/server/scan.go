package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sostav/api/internal/dict"
	"github.com/sostav/api/internal/imaging"
	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/pipeline"
	"github.com/sostav/api/internal/store"
)

// Параметры загрузки и нормализации фото (§8).
const (
	maxUploadBytes   = 10 << 20 // 10 МБ на исходное фото
	maxPhotos        = 3        // до 3 кадров одной этикетки на скан (§8)
	maxScanBodyBytes = 32 << 20 // 32 МБ на всё тело запроса (3×10 МБ + оверхед multipart)

	// multipartMemBytes — сколько тела ParseMultipartForm держит в RAM, прежде чем
	// спиллить остаток во временный файл. Это НЕ потолок размера (потолок —
	// maxScanBodyBytes выше), а порог «память против диска». Передавать сюда 32 МБ
	// нельзя: тогда каждый параллельный аплоад легально занимает до 32 МБ кучи, и
	// полсотни одновременных ужинов дают ~1.6 ГБ только на буферах. С 1 МБ фото
	// уезжает в os.TempDir и подчищается штатным MultipartForm.RemoveAll().
	multipartMemBytes = 1 << 20 // 1 МБ
	maxImageDim       = 1500    // длинная сторона после ресайза (меньше vision-токенов)
	jpegQuality       = 85

	// scanBusyRetryAfter — хинт фронту (сек) для ретрая при перегрузке (§15).
	scanBusyRetryAfter = "3"
)

// scanFlagView — флаг для фронта: заголовок, вычет и короткий вердикт (без диагнозов).
type scanFlagView struct {
	Title   string `json:"title"`
	Penalty int    `json:"penalty"`
	Verdict string `json:"verdict"`
	Risk    int    `json:"risk"`
}

// scanDeductionView — применённый вычет для сводки «почему такой балл». В отличие
// от flags (топ-3, вычет ДО потолка) — полный список и реально вычтенные числа:
// их сумма равна 100 − score, поэтому по ним можно показывать арифметику.
type scanDeductionView struct {
	Title   string `json:"title"`
	Penalty int    `json:"penalty"`
	Capped  bool   `json:"capped,omitempty"`
	// Происхождение штрафа: какой ингредиент, по какому правилу и на каком основании.
	// Ingredient и Source — для человека («почему такой балл»), Rule — машинный
	// идентификатор для поддержки, в интерфейсе он не показывается.
	Ingredient string `json:"ingredient,omitempty"`
	Code       string `json:"code,omitempty"`
	Rule       string `json:"rule,omitempty"`
	Source     string `json:"source,omitempty"`
}

// scanIngredientView — строка состава для фронта.
type scanIngredientView struct {
	Raw      string `json:"raw"`
	Name     string `json:"name,omitempty"`
	Code     string `json:"code,omitempty"`
	Risk     int    `json:"risk"`
	Position int    `json:"position"`
	Matched  bool   `json:"matched"`
	// Explanation — вердикт словаря для этой строки (§6); фронт разворачивает по тапу.
	// Пусто у несматченных — такие строки не тапаются.
	Explanation string `json:"explanation,omitempty"`
	// Basis — основание оценки (sources_note): разрешён ли, ДСП с источником.
	// Отвечает на «низкий по сравнению с чем»: бейдж — наша шкала (§6), а не доля
	// суточной нормы; долю не считаем, т.к. количество добавки на этикетке не печатают.
	Basis string `json:"basis,omitempty"`
}

// scanNutritionView — пищевая ценность (КБЖУ) для фронта. Числа — на 100 г/мл
// (basis); nil-поля опускаются. net_amount/net_unit — нетто всей упаковки: фронт
// сам считает «на всю упаковку» = на100 × net_amount/100, если единицы совпадают.
type scanNutritionView struct {
	Basis     string   `json:"basis,omitempty"`
	Kcal      *float64 `json:"kcal,omitempty"`
	ProteinG  *float64 `json:"protein_g,omitempty"`
	FatG      *float64 `json:"fat_g,omitempty"`
	CarbG     *float64 `json:"carb_g,omitempty"`
	NetAmount *float64 `json:"net_amount,omitempty"`
	NetUnit   string   `json:"net_unit,omitempty"`
}

type scanResponse struct {
	Status string `json:"status"`
	// Reason — уточнение статуса label_not_found (not_a_label | wrong_block |
	// unreadable): по нему фронт выбирает текст подсказки. Пусто — фронт падает
	// на общий текст (обратная совместимость со старым клиентом).
	Reason      string               `json:"reason,omitempty"`
	Score       int                  `json:"score"`
	Flags       []scanFlagView       `json:"flags"`
	Deductions  []scanDeductionView  `json:"deductions,omitempty"`
	Ingredients []scanIngredientView `json:"ingredients"`
	// LowConfidence — текст читался плохо: состав распознан, но мог быть прочитан с
	// ошибками. Фронт показывает плашку «сверьте состав с этикеткой». Уверенный скан
	// поля не содержит — отсутствие предупреждения и есть норма.
	LowConfidence bool `json:"low_confidence,omitempty"`
	// ScoreApproximate — плотная этикетка, прочитанная сильной моделью ДВАЖДЫ, дала два
	// разных набора штрафных добавок (pipeline/selfcheck.go). Балл показывается как
	// обычно, но с оговоркой «на этой этикетке он приблизителен» — там, где модель на
	// пределе, честнее сказать об этом, чем молчать одним числом. Смысловой аналог
	// знака «≈» в калориях. Совпали прочтения — поля нет (норма не объявляется).
	ScoreApproximate bool               `json:"score_approximate,omitempty"`
	Nutrition        *scanNutritionView `json:"nutrition,omitempty"`
	Explanation      string             `json:"explanation,omitempty"`
	Message          string             `json:"message,omitempty"`
	// ProductName — название продукта с упаковки. Пусто — не прочиталось (нормальный
	// исход: на кадре мог быть только оборот). Интерфейс подписывает им результат, а
	// при уходе в дневник — предзаполняет им название приёма.
	ProductName string `json:"product_name,omitempty"`
	// Transcription — ДОСЛОВНО прочитанный с этикетки блок состава. Показывается человеку
	// рядом с разбором, чтобы он мог заметить пропущенную или переврано прочитанную
	// строку: балл считается по распознанному тексту, и если текст неверен — неверно всё
	// ниже. Пусто — прочитать не удалось.
	Transcription string `json:"transcription,omitempty"`
	// RulesVersion — версия правил скоринга, по которой получен балл.
	RulesVersion string `json:"rules_version,omitempty"`
	// ScanNumber — порядковый номер успешной проверки пользователя (для фолбэка
	// ценника «ПРОВЕРКА №N», когда имя товара не извлекается). Только при status=ok.
	ScanNumber int `json:"scan_number,omitempty"`
	// ScansLeft — остаток бесплатных сканов. -1 == безлимит (подписчик): фронт
	// должен показывать «∞»/скрывать счётчик, а НЕ «-1 проверок» (Этап 4). Тот же
	// контракт, что и в meView (/api/auth, /api/me).
	ScansLeft int `json:"scans_left"`
}

// labelNotFoundMessage — текст под причину «не вижу состав». Дублирует тексты
// фронта осознанно: фронт рисует их сам (заголовок+подсказка), а message остаётся
// для клиентов без своей копирайт-таблицы и для старых версий mini-app.
func labelNotFoundMessage(reason string) string {
	switch reason {
	case pipeline.ReasonNotALabel:
		return "Это не похоже на состав продукта. Сфотографируйте блок «Состав» на упаковке."
	case pipeline.ReasonWrongBlock:
		return "Кажется, в кадре не тот блок. Нужен именно список ингредиентов."
	default: // unreadable и пустая причина
		return "Не получилось прочитать текст. Переснимите ближе и при хорошем свете."
	}
}

// handleScan — POST /api/scan (multipart, поле photo). Порядок (§8, §3):
// нормализация фото → кэш → пре-чек лимита → пайплайн → списание ТОЛЬКО при status=ok.
// label_not_found и error не списывают бесплатную попытку (в т.ч. когда сработал
// платный Sonnet-fallback, но состав так и не распознан — платит бизнес, не юзер).
//
// Кэш стоит ПЕРЕД пейволлом намеренно. Попадание в кэш — это фото, за которое
// пользователь уже заплатил попыткой; упереться на нём в «проверки кончились» значило
// бы отобрать оплаченное. Стоит это лишней нормализации кадров у того, кто и так за
// лимитом, — против vision-вызова цена пренебрежима, а потолки тела (≤32 МБ) на месте.
func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	ctx := r.Context()

	u, err := s.store.GetUserByID(ctx, claims.UserID)
	if err != nil {
		s.log.Error("scan: чтение пользователя", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}

	// Чтение фото из multipart. Поле photo может повторяться: 1–3 кадра ОДНОЙ этикетки
	// с разных ракурсов (круглая/изогнутая упаковка — §8). Все кадры уходят в ОДИН
	// vision-вызов, сохраняя порядок состава и не дублируя ингредиенты на стыке.
	// Потолки: ≤10 МБ на кадр, ≤3 кадра, ≤32 МБ на всё тело.
	r.Body = http.MaxBytesReader(w, r.Body, maxScanBodyBytes)
	if err := r.ParseMultipartForm(multipartMemBytes); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "приложите фото в поле photo (до 3 кадров, макс 10 МБ каждый)")
		return
	}
	defer r.MultipartForm.RemoveAll()

	headers := r.MultipartForm.File["photo"]
	switch {
	case len(headers) == 0:
		writeError(w, http.StatusBadRequest, "bad_request", "приложите фото в поле photo (макс 10 МБ)")
		return
	case len(headers) > maxPhotos:
		writeError(w, http.StatusBadRequest, "too_many_photos", "не больше 3 фото на один скан")
		return
	}

	images := make([]llm.Image, 0, len(headers))
	for _, fh := range headers {
		if fh.Size > maxUploadBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "photo_too_large", "каждое фото — не больше 10 МБ")
			return
		}
		f, err := fh.Open()
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "не удалось прочитать фото")
			return
		}
		raw, rerr := io.ReadAll(f)
		f.Close()
		if rerr != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "не удалось прочитать фото")
			return
		}
		// Нормализация: decode JPEG/PNG/WebP → ресайз ≤1500px → JPEG.
		jpg, nerr := imaging.Normalize(raw, maxImageDim, jpegQuality)
		if nerr != nil {
			writeError(w, http.StatusUnsupportedMediaType, "unsupported_media",
				"формат не поддерживается — пришлите JPEG, PNG или WebP")
			return
		}
		// Диагностика без сохранения фото (приватность не трогаем): по хешу видно, тот
		// же файл прислали повторно или разные. Разбор инцидента 2026-07-14, когда
		// webview слал не тот файл, чем снял пользователь.
		sum := sha256.Sum256(raw)
		s.log.Info("scan: принят кадр", "user", u.ID, "sha", hex.EncodeToString(sum[:6]),
			"in_bytes", len(raw), "out_bytes", len(jpg))

		images = append(images, llm.Image{Data: jpg, MIME: "image/jpeg"})
	}

	// Дебаг-захват (только локально, DEBUG_SAVE_SCANS): сохраняем ИМЕННО те кадры,
	// что уйдут в модель (после Normalize), — для разбора «модель выдала не тот состав».
	if s.cfg.DebugSaveScans {
		s.debugSaveScanImages(u.ID, images)
	}

	// Кэш разбора (analysis_cache.go): тот же файл — тот же ответ, без вызова модели
	// и без списания попытки. Проверяем ДО scan_started: попадание в кэш — это не
	// новая проверка, а показ уже сделанной, и в воронке сканов ему делать нечего.
	sha := imagesSHA(images)
	if cached, strength, hit := s.cachedScanResponse(ctx, u.ID, sha); hit {
		// Остаток проверок — из АКТУАЛЬНОГО состояния пользователя, а не из кэша:
		// за время между прогонами он мог потратить попытки на другие сканы или
		// оформить подписку.
		cached.ScansLeft = s.remainingScans(u)
		_ = s.store.InsertEvent(ctx, &u.ID, "scan_cache_hit", map[string]any{"status": cached.Status})
		s.log.Info("scan: попадание в кэш разбора", "user", u.ID, "sha", sha[:12])
		writeJSON(w, http.StatusOK, cached)

		// Разморозка: запись слабая, а детекторы говорят, что на этом кадре дешёвая
		// модель врёт. Ответ уже отдан — сильная модель переписывает запись фоном, и
		// следующее открытие того же фото покажет правду.
		//
		// Кадры отдавать в горутину безопасно: images — выход imaging.Normalize, то есть
		// собственные буферы в куче, не окна во временные файлы multipart (их подчищает
		// RemoveAll сразу после обработчика).
		if strength == store.CacheStrengthBase && s.pipe != nil {
			if dens := measureImages(images); suspiciousCachedScan(cached, dens, s.cfg.ScanForceEscalateFineLoss) {
				go s.thawScanCache(u.ID, sha, images, cached.Score)
			}
		}
		return
	}

	// Пре-чек лимита: неподписчик, исчерпавший бесплатные сканы, упирается в paywall.
	// Ниже кэша — см. комментарий к handleScan: уже проверенное фото пейволл не гасит.
	if acc := checkAccessWith(u, accessScan, time.Now(), s.accessLimits()); !acc.Allowed {
		_ = s.store.InsertEvent(ctx, &u.ID, "paywall_shown", map[string]any{"reason": acc.Reason})
		writeJSON(w, http.StatusPaymentRequired, paywallBody(acc, "scans_left"))
		return
	}

	_ = s.store.InsertEvent(ctx, &u.ID, "scan_started", nil)

	// Пайплайн (§4). res непустой даже при ошибке — пишем его в scans для диагностики.
	res, perr := s.pipe.Run(ctx, images)

	// Перегрузка (§15): семафор LLM-конкурентности исчерпан на входе — работа не
	// начиналась, поэтому НЕ пишем scan и НЕ списываем лимит; просим ретрай с бэкоффом.
	if errors.Is(perr, pipeline.ErrBusy) {
		w.Header().Set("Retry-After", scanBusyRetryAfter)
		writeError(w, http.StatusTooManyRequests, "busy", "высокая нагрузка, попробуйте через пару секунд")
		return
	}

	s.saveScan(ctx, u.ID, res, len(images))

	// Сила ответа для кэша: считается ОДИН раз здесь и передаётся во все ветки —
	// исход (ok / не еда / состава нет) на неё не влияет, влияет только то, чья модель
	// дала итоговое извлечение.
	strength := scanCacheStrength(res)

	switch {
	case perr != nil || res.Status == pipeline.StatusError:
		s.log.Error("scan: пайплайн", "err", perr, "user", u.ID)
		_ = s.store.InsertEvent(ctx, &u.ID, "scan_error", nil)
		writeError(w, http.StatusBadGateway, "scan_failed", "не удалось обработать фото, попробуйте ещё раз")

	case res.Status == pipeline.StatusMixedFrames:
		// Кадры от разных продуктов — попытку не списываем, просим снимать по одному.
		_ = s.store.InsertEvent(ctx, &u.ID, "scan_mixed_frames", nil)
		s.writeAndCacheScan(ctx, w, u.ID, sha, strength, scanResponse{
			Status:    res.Status,
			Message:   "Кадры от разных продуктов. Проверяйте по одному продукту за раз — несколько кадров нужны, только если состав одного продукта не влез в один снимок.",
			ScansLeft: s.remainingScans(u), // не списано
		})

	case res.Status == pipeline.StatusNotScored:
		// Алкоголь (§14.3): балла нет, но факты показываем — состав и КБЖУ, как их
		// прочитала модель. Состав отдаём СЫРЫМ (без нормализации по словарю): вердикты
		// по добавкам на этом экране не показываются, а фаза 2 стоит денег.
		_ = s.store.InsertEvent(ctx, &u.ID, "scan_not_scored", nil)
		ings := make([]scanIngredientView, 0, len(res.IngredientsRaw))
		for i, raw := range res.IngredientsRaw {
			ings = append(ings, scanIngredientView{Raw: raw, Position: i + 1})
		}
		s.writeAndCacheScan(ctx, w, u.ID, sha, strength, scanResponse{
			Status:      res.Status,
			Ingredients: ings,
			Nutrition:   nutritionView(res.Nutrition),
			ProductName: res.ProductName,
			Message:     "Наша методология оценивает пищевые добавки, а у алкоголя главный фактор — сам спирт, который она не учитывает. Балл был бы обманчив, поэтому мы его не ставим.",
			ScansLeft:   s.remainingScans(u), // не списано
		})

	case res.Status == pipeline.StatusNotFood:
		// Косметика/химия/лекарство — оценку съедобности не считаем, попытку не списываем.
		_ = s.store.InsertEvent(ctx, &u.ID, "scan_not_food", nil)
		s.writeAndCacheScan(ctx, w, u.ID, sha, strength, scanResponse{
			Status:    res.Status,
			Message:   "Похоже, это не еда — косметика, бытовая химия или другое несъедобное. Оценку состава для таких продуктов не считаем.",
			ScansLeft: s.remainingScans(u), // не списано
		})

	case res.Status == pipeline.StatusLabelNotFound:
		// Причину пишем в событие. Без неё «сканер не сработал» неразличимо расслаивается
		// на три разных случая: снял не этикетку (чаще всего — еду не в той вкладке), снял
		// не тот блок, не прочиталось. 06.09 по 14 провалам подряд нельзя было сказать,
		// что именно чинить, — props были nil, и пришлось догадываться по косвенным.
		_ = s.store.InsertEvent(ctx, &u.ID, "scan_label_not_found",
			map[string]any{"reason": res.Reason})
		s.writeAndCacheScan(ctx, w, u.ID, sha, strength, scanResponse{
			Status:    res.Status,
			Reason:    res.Reason,
			Message:   labelNotFoundMessage(res.Reason),
			ScansLeft: s.remainingScans(u), // не списано
		})

	default: // ok — списываем одну попытку атомарно
		newUsed, charged, cerr := s.store.ChargeFreeScan(ctx, u.ID, s.accessLimits().Scans)
		if cerr != nil {
			s.log.Error("scan: списание лимита", "err", cerr, "user", u.ID)
		}
		left := -1 // подписчик: безлимит
		if !u.IsSubscribed {
			if charged {
				left = s.accessLimits().Scans - newUsed
			} else {
				left = 0 // гонка увела за лимит между пре-чеком и списанием
			}
		}
		// Телеметрия антигаллюцинационного гейта (pipeline/transcription_gate.go): при
		// молчании гейта ("scan_ok" в норме) поля не добавляем — событие не разбухает.
		// Сработал — фиксируем частоту/эскалацию, чтобы через неделю посмотреть на
		// реальном потоке, как часто модель придумывает состав/КБЖУ.
		scanOkProps := map[string]any{"score": res.Score}
		addDetectorProps(scanOkProps, res)
		if res.GateMismatch {
			scanOkProps["gate_mismatch"] = true
			scanOkProps["gate_escalated"] = res.GateEscalated
			scanOkProps["gate_nutrition_mismatch"] = res.GateNutritionMismatch
		}
		_ = s.store.InsertEvent(ctx, &u.ID, "scan_ok", scanOkProps)
		// Обязательное событие воронки (ТЗ §10). Ни состава, ни фото — только длина
		// распознанного текста: она отвечает на «сколько удалось прочитать», не раскрывая
		// что именно.
		s.track(ctx, u.ID, evLabelAnalysisSuccess, map[string]any{
			"model_version": res.Provider,
			"ocr_length":    len(res.Transcription),
			"rules_version": res.RulesVersion,
			"latency_ms":    res.LatencyMs,
			"cost":          res.CostRUB,
		})
		// Порядковый номер проверки для фолбэка ценника (best-effort — 0 просто опускается).
		num, nerr := s.store.CountUserScansOK(ctx, u.ID)
		if nerr != nil {
			s.log.Warn("scan: подсчёт номера проверки", "err", nerr, "user", u.ID)
		}
		s.writeAndCacheScan(ctx, w, u.ID, sha, strength, buildScanResponse(res, left, num))
	}
}

// buildScanResponse собирает клиентский ответ из результата пайплайна.
func buildScanResponse(res *pipeline.Result, scansLeft, scanNumber int) scanResponse {
	flags := make([]scanFlagView, 0, len(res.Flags))
	for _, f := range res.Flags {
		flags = append(flags, scanFlagView{Title: f.Title, Penalty: f.Penalty, Verdict: f.Reason, Risk: f.Risk})
	}
	ings := make([]scanIngredientView, 0, len(res.Matched))
	for _, m := range res.Matched {
		ings = append(ings, scanIngredientView{
			Raw: m.Raw, Name: m.Name, Code: m.Code, Risk: m.Risk, Position: m.Position, Matched: m.Matched,
			Explanation: m.Explanation, Basis: m.Basis,
		})
	}
	deds := make([]scanDeductionView, 0, len(res.Breakdown))
	for _, d := range res.Breakdown {
		deds = append(deds, scanDeductionView{
			Title: d.Title, Penalty: d.Penalty, Capped: d.Capped,
			Ingredient: d.Ingredient, Code: d.Code, Rule: d.Rule, Source: d.Source,
		})
	}
	return scanResponse{
		Status: res.Status, Score: res.Score, Flags: flags, Ingredients: ings,
		Deductions:       deds,
		Transcription:    res.Transcription,
		RulesVersion:     res.RulesVersion,
		LowConfidence:    res.LowConfidence,
		ScoreApproximate: res.ScoreApproximate,
		Nutrition:        nutritionView(res.Nutrition),
		ProductName:      res.ProductName,
		Explanation:      res.Explanation, ScanNumber: scanNumber, ScansLeft: scansLeft,
	}
}

// nutritionView отдаёт КБЖУ фронту только когда есть хоть одно осмысленное число
// (иначе блок не показываем — не рисуем пустую таблицу).
func nutritionView(n *llm.Nutrition) *scanNutritionView {
	if !n.HasValues() {
		return nil
	}
	return &scanNutritionView{
		Basis: n.Basis, Kcal: n.Kcal, ProteinG: n.ProteinG, FatG: n.FatG, CarbG: n.CarbG,
		NetAmount: n.NetAmount, NetUnit: n.NetUnit,
	}
}

// remainingScans — остаток бесплатных сканов без списания (подписчик → -1).
func (s *Server) remainingScans(u store.User) int {
	// Через тот же checkAccess, что и гейт: остаток на экране обязан сходиться с тем,
	// пустит ли кнопка. Кончился СРОК — остаток ноль, сколько бы сканов ни осталось.
	if acc := checkAccessWith(u, accessScan, time.Now(), s.accessLimits()); acc.Left > 0 || acc.Left == -1 {
		return acc.Left
	}
	return 0
}

// debugSaveScanImages пишет нормализованные кадры скана на диск (только при
// DEBUG_SAVE_SCANS, локальный разбор распознавания). Best-effort: ошибки логируем,
// скан не роняем. Имя: scan_<unixnano>_u<userID>_<n>.jpg. Файлы чистить вручную.
func (s *Server) debugSaveScanImages(userID int64, images []llm.Image) {
	dir := s.cfg.DebugScanDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.log.Warn("debug: создание папки кадров", "err", err, "dir", dir)
		return
	}
	ts := time.Now().UnixNano()
	for i, img := range images {
		path := filepath.Join(dir, fmt.Sprintf("scan_%d_u%d_%d.jpg", ts, userID, i+1))
		if err := os.WriteFile(path, img.Data, 0o644); err != nil {
			s.log.Warn("debug: запись кадра", "err", err, "path", path)
			continue
		}
		s.log.Info("debug: сохранён кадр скана", "path", path, "bytes", len(img.Data))
	}
}

// saveScan пишет результат скана (best-effort — диагностика не должна ронять ответ).
// frames — сколько кадров прислал пользователь: без этого числа нельзя отличить
// «нутриент-ретрай промахнулся» от «ретрай штатно пропущен» (он только для 1 кадра).
func (s *Server) saveScan(ctx context.Context, userID int64, res *pipeline.Result, frames int) {
	var score *int
	if res.Status == pipeline.StatusOK {
		v := res.Score
		score = &v
	}
	flags, _ := json.Marshal(res.Flags)
	rawIng, _ := json.Marshal(res.IngredientsRaw)
	matched, _ := json.Marshal(res.Matched)
	// Пишем КБЖУ ровно в том виде, в каком он уходит клиенту (nutritionView): пустой
	// набор — это NULL, а не «{}» — иначе аналитика по nutrition IS NOT NULL считала бы
	// пустышки за найденные таблицы.
	var nutrition []byte
	if nv := nutritionView(res.Nutrition); nv != nil {
		nutrition, _ = json.Marshal(nv)
	}
	// Сырой сигнал фазы 1 — с флагами present/visible, которые nutritionView отбрасывает.
	// Именно он различает «таблицы не было в кадре» и «была, но числа не прочитались».
	var nutritionRaw []byte
	if res.Nutrition != nil {
		nutritionRaw, _ = json.Marshal(res.Nutrition)
	}
	scanID, err := s.store.InsertScan(ctx, store.ScanRow{
		UserID: userID, Status: res.Status, Score: score,
		Flags: flags, IngredientsRaw: rawIng, IngredientsMatched: matched,
		Nutrition: nutrition, NutritionRaw: nutritionRaw, Frames: frames,
		Provider: res.Provider, LatencyMs: res.LatencyMs, CostRUB: res.CostRUB,
	})
	if err != nil {
		s.log.Error("scan: сохранение в scans", "err", err, "user", userID)
		return // без id скана привязывать несматченные строки не к чему
	}
	s.recordUnmatched(ctx, scanID, res)
}

// recordUnmatched копит строки состава, которые не опознал ни словарь, ни добор
// LLM (§6): из них еженедельно пополняется словарь. Только для успешных сканов —
// у ошибочных и «не вижу состав» в Matched либо пусто, либо мусор распознавания.
// Best-effort: сбор статистики не имеет права ронять ответ пользователю.
func (s *Server) recordUnmatched(ctx context.Context, scanID int64, res *pipeline.Result) {
	if res.Status != pipeline.StatusOK {
		return
	}
	seen := make(map[string]struct{}, len(res.Matched))
	items := make([]store.UnmatchedIngredient, 0, len(res.Matched))
	for _, m := range res.Matched {
		if m.Matched {
			continue
		}
		raw := strings.TrimSpace(m.Raw)
		if raw == "" {
			continue
		}
		// Дедуп внутри одного скана: одна и та же строка, встреченная дважды на этикетке,
		// не должна давать +2 к счётчику встреч — счётчик меряет сканы, а не строки.
		if _, dup := seen[raw]; dup {
			continue
		}
		seen[raw] = struct{}{}
		items = append(items, store.UnmatchedIngredient{Raw: raw, Normalized: dict.Normalize(raw)})
	}
	if len(items) == 0 {
		return
	}
	if err := s.store.RecordUnmatchedIngredients(ctx, scanID, items); err != nil {
		s.log.Warn("scan: запись несматченных ингредиентов", "err", err, "scan", scanID, "n", len(items))
	}
}
