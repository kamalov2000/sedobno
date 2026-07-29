package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/meal"
	"github.com/sostav/api/internal/pipeline"
	"github.com/sostav/api/internal/store"
)

// Кэш разбора по хэшу изображения (store/cache.go, миграция 0016).
//
// Зачем: одно и то же фото, отправленное дважды, обязано дать один и тот же ответ.
// Пока это не так, пользователь видит не «погрешность модели», а враньё программы —
// он-то знает, что фото не менял (живой кейс: онигири, три прогона, 81/38/68).
//
// Кэшируется РЕЗУЛЬТАТ РАЗБОРА, а не HTTP-ответ целиком: остаток квоты
// (scans_left/meals_left) зависит от текущего состояния пользователя и подставляется
// заново на каждом попадании. Что именно лежит в кэше, у двух сканеров различается —
// потому что различаются побочные эффекты успешного разбора:
//
//   - сканер составов: кладём готовый scanResponse (вместе с scan_number). Повторный
//     скан того же файла — это ТА ЖЕ проверка, поэтому новой строки в истории не
//     появляется и номер проверки не растёт;
//   - счётчик калорий: кладём meal.Result. Повторная отправка того же фото — всё
//     равно добавление приёма в дневник (человек мог съесть это второй раз), поэтому
//     запись в meals создаётся, а вот модель не вызывается и квота не списывается.
//
// Общее для обоих: попадание не тратит ни квоту, ни деньги на LLM, и отвечает мгновенно.

// imagesSHA — ключ кэша: sha256 по НОРМАЛИЗОВАННЫМ кадрам (после imaging.Normalize),
// в порядке отправки, hex.
//
// Почему нормализованные, а не исходные байты: в модель уходят именно они, и ключ
// обязан совпадать с тем, что модель реально видела. Побочно это склеивает случаи,
// когда клиент пережал один и тот же снимок с другим качеством, но дал ту же картинку
// после ресайза.
//
// Мультикадр даёт ОДИН хэш на весь набор: разбор считается по набору целиком, и три
// кадра в другом порядке — это другой вход. Длина каждого кадра подмешивается в хэш,
// чтобы склейка была однозначной (иначе кадры [AB][C] и [A][BC] дали бы один ключ).
func imagesSHA(images []llm.Image) string {
	h := sha256.New()
	var lenBuf [8]byte
	for _, img := range images {
		n := uint64(len(img.Data))
		for i := 0; i < 8; i++ {
			lenBuf[i] = byte(n >> (8 * i))
		}
		h.Write(lenBuf[:])
		h.Write(img.Data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// cachedScanResponse достаёт сохранённый ответ сканера. Ошибки кэша не фатальны:
// не прочитали — просто считаем заново (best-effort, как вся диагностика).
func (s *Server) cachedScanResponse(ctx context.Context, userID int64, sha string) (scanResponse, bool) {
	raw, found, err := s.store.GetAnalysisCache(ctx, userID, store.CacheKindScan, sha)
	if err != nil {
		s.log.Warn("scan: чтение кэша разбора", "err", err, "user", userID)
		return scanResponse{}, false
	}
	if !found {
		return scanResponse{}, false
	}
	var resp scanResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		s.log.Warn("scan: разбор кэша", "err", err, "user", userID)
		return scanResponse{}, false
	}
	return resp, true
}

// scanCacheStrength — сила ответа сканера для кэша. Сильный только тот разбор, чьё
// ИТОГОВОЕ извлечение пришло от Sonnet: звали сильную модель или нет — неважно, важно,
// чей ответ мы показываем.
func scanCacheStrength(res *pipeline.Result) int16 {
	if res != nil && res.StrongAnswer {
		return store.CacheStrengthStrong
	}
	return store.CacheStrengthBase
}

// writeAndCacheScan отвечает клиенту и кладёт ответ в кэш. Статус error сюда не
// попадает намеренно (обработчик отвечает на него writeError): сбой пайплайна —
// состояние преходящее, замораживать его на 90 дней нельзя, иначе одна сетевая
// неудача навсегда сломала бы конкретное фото.
//
// strength решает конфликт с уже лежащей записью: сильный ответ вытесняет слабый,
// слабый поверх сильного не пишется (store.PutAnalysisCache).
func (s *Server) writeAndCacheScan(ctx context.Context, w http.ResponseWriter, userID int64, sha string, strength int16, resp scanResponse) {
	body, err := json.Marshal(resp)
	if err == nil {
		if perr := s.store.PutAnalysisCache(ctx, userID, store.CacheKindScan, sha, body, strength); perr != nil {
			s.log.Warn("scan: запись кэша разбора", "err", perr, "user", userID)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// cachedMealResult достаёт сохранённый разбор еды.
func (s *Server) cachedMealResult(ctx context.Context, userID int64, sha string) (*meal.Result, bool) {
	raw, found, err := s.store.GetAnalysisCache(ctx, userID, store.CacheKindMeal, sha)
	if err != nil {
		s.log.Warn("meal: чтение кэша разбора", "err", err, "user", userID)
		return nil, false
	}
	if !found {
		return nil, false
	}
	var res meal.Result
	if err := json.Unmarshal(raw, &res); err != nil {
		s.log.Warn("meal: разбор кэша", "err", err, "user", userID)
		return nil, false
	}
	return &res, true
}

// cacheMealResult сохраняет разбор еды. Как и у сканера, статус error не кэшируется,
// а сила ответа решает конфликт с уже лежащей записью. У калорий признак прямой —
// res.Escalated: разбор целиком выполнила сильная модель, частичной подмены, как у
// сканера, здесь нет.
func (s *Server) cacheMealResult(ctx context.Context, userID int64, sha string, res *meal.Result) {
	body, err := json.Marshal(res)
	if err != nil {
		return
	}
	strength := store.CacheStrengthBase
	if res.Escalated {
		strength = store.CacheStrengthStrong
	}
	if perr := s.store.PutAnalysisCache(ctx, userID, store.CacheKindMeal, sha, body, strength); perr != nil {
		s.log.Warn("meal: запись кэша разбора", "err", perr, "user", userID)
	}
}
