package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

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
	maxImageDim      = 1500     // длинная сторона после ресайза (меньше vision-токенов)
	jpegQuality      = 85

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
	Status      string               `json:"status"`
	Score       int                  `json:"score"`
	Flags       []scanFlagView       `json:"flags"`
	Ingredients []scanIngredientView `json:"ingredients"`
	Nutrition   *scanNutritionView   `json:"nutrition,omitempty"`
	Explanation string               `json:"explanation,omitempty"`
	Message     string               `json:"message,omitempty"`
	// ScanNumber — порядковый номер успешной проверки пользователя (для фолбэка
	// ценника «ПРОВЕРКА №N», когда имя товара не извлекается). Только при status=ok.
	ScanNumber int `json:"scan_number,omitempty"`
	// ScansLeft — остаток бесплатных сканов. -1 == безлимит (подписчик): фронт
	// должен показывать «∞»/скрывать счётчик, а НЕ «-1 проверок» (Этап 4). Тот же
	// контракт, что и в meView (/api/auth, /api/me).
	ScansLeft int `json:"scans_left"`
}

// handleScan — POST /api/scan (multipart, поле photo). Порядок (§8, §3):
// пре-чек лимита → нормализация фото → пайплайн → списание ТОЛЬКО при status=ok.
// label_not_found и error не списывают бесплатную попытку (в т.ч. когда сработал
// платный Sonnet-fallback, но состав так и не распознан — платит бизнес, не юзер).
func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	ctx := r.Context()

	// Пре-чек лимита: неподписчик, исчерпавший бесплатные сканы, упирается в paywall.
	u, err := s.store.GetUserByID(ctx, claims.UserID)
	if err != nil {
		s.log.Error("scan: чтение пользователя", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}
	if !u.IsSubscribed && u.FreeScansUsed >= freeScanLimit {
		_ = s.store.InsertEvent(ctx, &u.ID, "paywall_shown", nil)
		writeJSON(w, http.StatusPaymentRequired, map[string]any{
			"error": "limit_reached", "message": "бесплатные сканы закончились", "scans_left": 0,
		})
		return
	}

	// Чтение фото из multipart. Поле photo может повторяться: 1–3 кадра ОДНОЙ этикетки
	// с разных ракурсов (круглая/изогнутая упаковка — §8). Все кадры уходят в ОДИН
	// vision-вызов, сохраняя порядок состава и не дублируя ингредиенты на стыке.
	// Потолки: ≤10 МБ на кадр, ≤3 кадра, ≤32 МБ на всё тело.
	r.Body = http.MaxBytesReader(w, r.Body, maxScanBodyBytes)
	if err := r.ParseMultipartForm(maxScanBodyBytes); err != nil {
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
		images = append(images, llm.Image{Data: jpg, MIME: "image/jpeg"})
	}

	// Дебаг-захват (только локально, DEBUG_SAVE_SCANS): сохраняем ИМЕННО те кадры,
	// что уйдут в модель (после Normalize), — для разбора «модель выдала не тот состав».
	if s.cfg.DebugSaveScans {
		s.debugSaveScanImages(u.ID, images)
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

	s.saveScan(ctx, u.ID, res)

	switch {
	case perr != nil || res.Status == pipeline.StatusError:
		s.log.Error("scan: пайплайн", "err", perr, "user", u.ID)
		_ = s.store.InsertEvent(ctx, &u.ID, "scan_error", nil)
		writeError(w, http.StatusBadGateway, "scan_failed", "не удалось обработать фото, попробуйте ещё раз")

	case res.Status == pipeline.StatusMixedFrames:
		// Кадры от разных продуктов — попытку не списываем, просим снимать по одному.
		_ = s.store.InsertEvent(ctx, &u.ID, "scan_mixed_frames", nil)
		writeJSON(w, http.StatusOK, scanResponse{
			Status:    res.Status,
			Message:   "Кадры от разных продуктов. Проверяйте по одному продукту за раз — несколько кадров нужны, только если состав одного продукта не влез в один снимок.",
			ScansLeft: remainingScans(u), // не списано
		})

	case res.Status == pipeline.StatusNotFood:
		// Косметика/химия/лекарство — оценку съедобности не считаем, попытку не списываем.
		_ = s.store.InsertEvent(ctx, &u.ID, "scan_not_food", nil)
		writeJSON(w, http.StatusOK, scanResponse{
			Status:    res.Status,
			Message:   "Похоже, это не еда — косметика, бытовая химия или другое несъедобное. Оценку состава для таких продуктов не считаем.",
			ScansLeft: remainingScans(u), // не списано
		})

	case res.Status == pipeline.StatusLabelNotFound:
		_ = s.store.InsertEvent(ctx, &u.ID, "scan_label_not_found", nil)
		writeJSON(w, http.StatusOK, scanResponse{
			Status:    res.Status,
			Message:   "Не разглядел состав. Снимите блок «Состав» крупно и при хорошем свете.",
			ScansLeft: remainingScans(u), // не списано
		})

	default: // ok — списываем одну попытку атомарно
		newUsed, charged, cerr := s.store.ChargeFreeScan(ctx, u.ID, freeScanLimit)
		if cerr != nil {
			s.log.Error("scan: списание лимита", "err", cerr, "user", u.ID)
		}
		left := -1 // подписчик: безлимит
		if !u.IsSubscribed {
			if charged {
				left = freeScanLimit - newUsed
			} else {
				left = 0 // гонка увела за лимит между пре-чеком и списанием
			}
		}
		_ = s.store.InsertEvent(ctx, &u.ID, "scan_ok", map[string]any{"score": res.Score})
		// Порядковый номер проверки для фолбэка ценника (best-effort — 0 просто опускается).
		num, nerr := s.store.CountUserScansOK(ctx, u.ID)
		if nerr != nil {
			s.log.Warn("scan: подсчёт номера проверки", "err", nerr, "user", u.ID)
		}
		writeJSON(w, http.StatusOK, buildScanResponse(res, left, num))
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
	return scanResponse{
		Status: res.Status, Score: res.Score, Flags: flags, Ingredients: ings,
		Nutrition:   nutritionView(res.Nutrition),
		Explanation: res.Explanation, ScanNumber: scanNumber, ScansLeft: scansLeft,
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
func remainingScans(u store.User) int {
	if u.IsSubscribed {
		return -1
	}
	if left := freeScanLimit - u.FreeScansUsed; left > 0 {
		return left
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
func (s *Server) saveScan(ctx context.Context, userID int64, res *pipeline.Result) {
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
	err := s.store.InsertScan(ctx, store.ScanRow{
		UserID: userID, Status: res.Status, Score: score,
		Flags: flags, IngredientsRaw: rawIng, IngredientsMatched: matched,
		Nutrition: nutrition,
		Provider:  res.Provider, LatencyMs: res.LatencyMs, CostRUB: res.CostRUB,
	})
	if err != nil {
		s.log.Error("scan: сохранение в scans", "err", err, "user", userID)
	}
}
