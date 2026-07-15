package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/pipeline"
	"github.com/sostav/api/internal/store"
)

// История сканов (§14.4). Читаем уже сохранённые сканы пользователя и восстанавливаем
// из них тот же ответ, что отдаёт живой скан (переиспользуем buildScanResponse) — балл
// и разбивку вычетов пересчитываем из ingredients_matched (Pipeline.Rescore), ничего
// нового не храня. Фото не было и нет — в ответе его тоже нет.

const (
	historyDefaultLimit = 20
	historyMaxLimit     = 50
)

// historyItem — карточка истории: восстановленный ответ скана + его id и дата.
type historyItem struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	scanResponse
}

type historyResponse struct {
	Items []historyItem `json:"items"`
	// NextCursor — id, который клиент передаст как ?cursor= для следующей страницы.
	// 0/отсутствует — больше сканов нет.
	NextCursor int64 `json:"next_cursor,omitempty"`
}

// handleScansHistory — GET /api/scans/history?cursor=<id>&limit=<n> (защищённый).
func (s *Server) handleScansHistory(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "нужна авторизация")
		return
	}
	ctx := r.Context()

	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	if cursor < 0 {
		cursor = 0
	}
	limit := historyDefaultLimit
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	if limit > historyMaxLimit {
		limit = historyMaxLimit
	}

	// Берём на одну строку больше запрошенного: лишняя строка = «есть следующая
	// страница», её же id — курсор. Так пустой «хвост» не показываем.
	rows, err := s.store.ListScans(ctx, claims.UserID, cursor, limit+1)
	if err != nil {
		s.log.Error("history: чтение сканов", "err", err, "user", claims.UserID)
		writeError(w, http.StatusInternalServerError, "internal", "внутренняя ошибка")
		return
	}

	var next int64
	if len(rows) > limit {
		next = rows[limit-1].ID
		rows = rows[:limit]
	}

	items := make([]historyItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, historyItem{
			ID:           row.ID,
			CreatedAt:    row.CreatedAt,
			scanResponse: s.rebuildScanView(row),
		})
	}
	writeJSON(w, http.StatusOK, historyResponse{Items: items, NextCursor: next})
}

// rebuildScanView восстанавливает ответ скана из сохранённой строки: балл, флаги и
// разбивку вычетов считает заново тем же скорером (Rescore), КБЖУ и состав берёт как
// есть. low_confidence и текст-объяснение мы не храним — в истории их просто нет.
func (s *Server) rebuildScanView(row store.ScanHistoryRow) scanResponse {
	var nut *llm.Nutrition
	if len(row.NutritionRaw) > 0 {
		_ = json.Unmarshal(row.NutritionRaw, &nut)
	}

	switch row.Status {
	case pipeline.StatusOK:
		var matched []pipeline.MatchedIngredient
		if len(row.IngredientsMatched) > 0 {
			_ = json.Unmarshal(row.IngredientsMatched, &matched)
		}
		sc := s.pipe.Rescore(matched) // балл и разбивку считаем заново из состава
		res := &pipeline.Result{
			Status: row.Status, Score: sc.Score, Flags: sc.Flags,
			Breakdown: sc.Breakdown, Matched: matched, Nutrition: nut,
		}
		return buildScanResponse(res, 0, 0) // scansLeft/scanNumber в истории не нужны

	case pipeline.StatusNotScored:
		// Алкоголь: балла нет, но факты показываем. Состав лежит в raw (matched пуст —
		// пайплайн вышел до фазы 2), как и в живом ответе. Текст берёт дефолт NotScoredScreen.
		var raw []string
		if len(row.IngredientsRaw) > 0 {
			_ = json.Unmarshal(row.IngredientsRaw, &raw)
		}
		ings := make([]scanIngredientView, 0, len(raw))
		for i, r := range raw {
			ings = append(ings, scanIngredientView{Raw: r, Position: i + 1})
		}
		return scanResponse{Status: row.Status, Ingredients: ings, Nutrition: nutritionView(nut)}

	default:
		// label_not_found / not_food / mixed_frames / error — распознавания нет,
		// показываем как карточку статуса без деталей.
		return scanResponse{Status: row.Status}
	}
}
