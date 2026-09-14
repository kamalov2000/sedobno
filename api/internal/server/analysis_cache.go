package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"time"

	"github.com/sostav/api/internal/imaging"
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
func (s *Server) cachedScanResponse(ctx context.Context, userID int64, sha string) (scanResponse, int16, bool) {
	raw, strength, found, err := s.store.GetAnalysisCache(ctx, userID, store.CacheKindScan, sha)
	if err != nil {
		s.log.Warn("scan: чтение кэша разбора", "err", err, "user", userID)
		return scanResponse{}, 0, false
	}
	if !found {
		return scanResponse{}, 0, false
	}
	var resp scanResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		s.log.Warn("scan: разбор кэша", "err", err, "user", userID)
		return scanResponse{}, 0, false
	}
	if scanNegativeAboutLabel(resp) {
		// Запись из времён, когда такие отказы ещё замораживались: отдавать её — значит ещё
		// 90 дней повторять промах модели. Убираем и считаем промахом кэша.
		s.dropNegativeCache(ctx, userID, store.CacheKindScan, sha, resp.Status+"/"+resp.Reason)
		return scanResponse{}, 0, false
	}
	return resp, strength, true
}

// Отрицательный вердикт о читаемом составе не кэшируется (backlog п.12, сценарий 2,
// 14.09.2026). «Состава не видно» — не свойство кадра, а промах читающей модели: на
// обороте сметаны с крупным «Состав: …» калории ответили package_front, и отказ лёг в
// кэш на 90 дней. Такие ответы не пишем, а уже лежащие — не отдаём и убираем.
//
// Что НЕ считается таким вердиктом и кэшируется по-прежнему: успешные разборы, «не еда»
// (уверенное опознание кадра), «только блюдо» у сканера (это опознание еды, а не вывод о
// составе — и именно оно даёт мгновенный переброс в калории при повторе) и «непонятный
// кадр» калорий (не про состав).
func mealNegativeAboutLabel(status string) bool {
	return status == meal.StatusPackageFront || status == meal.StatusLabelUnreadable
}

func scanNegativeAboutLabel(resp scanResponse) bool {
	return resp.Status == pipeline.StatusLabelNotFound && resp.Reason != pipeline.ReasonDishOnly
}

// dropNegativeCache убирает негодную запись и оставляет след в событиях: по ним видно,
// сколько старых отказов реально всплыло после правки. Ошибка удаления не фатальна —
// запись всё равно не отдаётся.
func (s *Server) dropNegativeCache(ctx context.Context, userID int64, kind, sha, verdict string) {
	if err := s.store.DeleteAnalysisCache(ctx, userID, kind, sha); err != nil {
		s.log.Warn("кэш: удаление отрицательной записи", "err", err, "user", userID, "kind", kind)
	}
	_ = s.store.InsertEvent(ctx, &userID, "cache_negative_dropped", map[string]any{"kind": kind, "verdict": verdict})
	s.log.Info("кэш: старый отрицательный вердикт о составе не отдан и удалён",
		"user", userID, "kind", kind, "verdict", verdict, "sha", sha[:12])
}

// thawCacheTimeout — потолок фоновой перепроверки. Больше обычного скана: она идёт
// через сильную модель, у которой латентность втрое выше (замер: 6.3 с против 17.9 с).
const thawCacheTimeout = 90 * time.Second

// thawScanCache — фоновая разморозка слабой записи кэша.
//
// Задача: кэш заморозил ответ дешёвой модели, а детекторы говорят, что на этом кадре
// она врёт. Ждать пользователя нельзя (ответ уже отдан мгновенно — в этом весь смысл
// кэша), а оставлять враньё лежать 90 дней тем более. Поэтому сильная модель
// доигрывает партию ПОСЛЕ ответа и переписывает запись: следующее открытие того же
// фото покажет уже правду. Максимум один показ вранья вместо бессрочного.
//
// Контекст свой, не запросный: запросный отменяется сразу после ответа клиенту, и
// разморозка умирала бы, не начавшись. Паника гасится здесь же — фоновая горутина не
// имеет права уронить процесс.
func (s *Server) thawScanCache(userID int64, sha string, images []llm.Image, was int) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("разморозка кэша: паника", "recover", r, "user", userID)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), thawCacheTimeout)
	defer cancel()

	// Разморозка идёт в фоне, без попытки человека (op пуст), но платим за неё мы —
	// значит, в журнале она должна быть (backlog п.9).
	// У фоновой задачи попытки человека нет, но пометить проверочный вызов надо так же,
	// как в обработчике: иначе разморозка кэша синтетического аккаунта попадёт в отчёт.
	isTest := false
	if u, uerr := s.store.GetUserByID(ctx, userID); uerr == nil {
		isTest = isTestTelegramID(u.TelegramID)
	}
	calls := s.newCallLog(userID, "", "scan", isTest)
	defer calls.flush()
	ctx = llm.WithRecorder(ctx, calls)

	// Обычный Run: детекторы внутри пайплайна сработают на тех же кадрах и сами уведут
	// разбор на сильную модель. Отдельного «принудительного» режима не заводим — иначе
	// правило эскалации существовало бы в двух местах и разъехалось бы.
	res, err := s.pipe.Run(ctx, images)
	if err != nil {
		s.log.Warn("разморозка кэша: пайплайн", "err", err, "user", userID)
		return
	}
	if !res.StrongAnswer {
		// Сильная модель так и не отработала (эскалация не сложилась). Писать слабый
		// ответ поверх слабого незачем — только сбросили бы TTL и счётчик попаданий.
		s.log.Info("разморозка кэша: сильный ответ не получен, запись не трогаем", "user", userID)
		return
	}

	num, _ := s.store.CountUserScansOK(ctx, userID)
	resp := buildScanResponse(res, 0, num) // scans_left подставляется на выдаче
	body, merr := json.Marshal(resp)
	if merr != nil {
		return
	}
	if perr := s.store.PutAnalysisCache(ctx, userID, store.CacheKindScan, sha, body, store.CacheStrengthStrong); perr != nil {
		s.log.Warn("разморозка кэша: запись", "err", perr, "user", userID)
		return
	}
	// Стоимость и латентность — В СОБЫТИЕ, потому что строки в scans разморозка не
	// создаёт (это не проверка пользователя: она не должна попадать в его историю и
	// двигать номер проверки). Без этих полей фоновые прогоны были бы единственной
	// статьёй расходов на LLM, которой нет нигде в учёте.
	props := map[string]any{
		"score_was": was, "score_now": res.Score,
		"cost_rub": math.Round(res.CostRUB*10000) / 10000, "latency_ms": res.LatencyMs,
	}
	addDetectorProps(props, res)
	_ = s.store.InsertEvent(ctx, &userID, "scan_cache_thawed", props)
	s.log.Info("разморозка кэша: запись обновлена сильной моделью",
		"user", userID, "было", was, "стало", res.Score)
}

// measureImages — максимум признаков плотности по набору кадров. Максимум, а не
// среднее: состав может быть на одном плотном кадре, и «обзорное фото упаковки»
// рядом не должно его разбавлять. Кадр, который не померился, просто не участвует.
func measureImages(images []llm.Image) imaging.Density {
	var out imaging.Density
	for _, img := range images {
		d, err := imaging.Measure(img.Data)
		if err != nil {
			continue
		}
		if d.FineLoss > out.FineLoss {
			out.FineLoss = d.FineLoss
		}
		if d.EdgeFrac > out.EdgeFrac {
			out.EdgeFrac = d.EdgeFrac
		}
	}
	return out
}

// addDetectorProps подмешивает в событие признаки детекторов сочинённого состава.
// Пишутся на КАЖДОМ скане, а не только на сработавших: порог форс-эскалации выбран
// на корпусе из двух десятков кадров одной фотосессии, и перекалибровать его можно
// только по распределению живого трафика. Без «несработавших» замеров у распределения
// нет левой половины, и любой новый порог был бы такой же догадкой, как первый.
//
// Числа округляем до трёх знаков: в events это сырьё для гистограммы, а не для
// побитового сравнения, и полная мантисса раздувала бы props без пользы.
func addDetectorProps(props map[string]any, res *pipeline.Result) {
	if res == nil {
		return
	}
	props["fine_loss"] = math.Round(res.FineLoss*1000) / 1000
	props["edge_frac"] = math.Round(res.EdgeFrac*1000) / 1000
	props["chars_per_item"] = math.Round(res.CharsPerItem*10) / 10
	if res.ForcedStrong {
		props["forced_strong"] = true
	}
	if res.ShortItemsFlag {
		props["short_items"] = true
	}
	// Прицельный self-consistency (pipeline/selfcheck.go). Пишем ОБА балла и обе
	// половины расхождения, а не только факт срабатывания: открытый вопрос бэклога —
	// как часто и на чём именно сильная модель расходится сама с собой, и ответить на
	// него можно лишь по накопленной выборке флагнутых сканов. Коды короткие (E-номера),
	// props от них не разбухают.
	if res.SelfCheckRan {
		props["sc_ran"] = true
		props["sc_score_b"] = res.SelfCheckScore
		if res.ScoreApproximate {
			props["sc_diverged"] = true
			props["sc_only_a"] = res.SelfCheckOnlyA
			props["sc_only_b"] = res.SelfCheckOnlyB
		}
	}
}

// suspiciousCachedScan — стоит ли перепроверять СЛАБУЮ запись кэша сильной моделью.
// Оба детектора считаются без единого токена: плотность — по кадрам, форма состава —
// по уже сохранённому ответу.
func suspiciousCachedScan(resp scanResponse, dens imaging.Density, threshold float64) bool {
	if resp.Status != pipeline.StatusOK {
		return false // перепроверять «не еда»/«состава нет» нечего
	}
	if threshold > 0 && dens.FineLoss >= threshold {
		return true
	}
	items := make([]string, 0, len(resp.Ingredients))
	for _, ing := range resp.Ingredients {
		items = append(items, ing.Raw)
	}
	return pipeline.ShortItemsSuspicious(items, dens.FineLoss)
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
	if scanNegativeAboutLabel(resp) {
		writeJSON(w, http.StatusOK, resp) // отказ «состава не видно» не замораживаем
		return
	}
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
	// strength у калорий не используется: детекторов сочинённого состава для фото еды
	// нет (там нет печатного текста, который можно недочитать), и размораживать нечего.
	raw, _, found, err := s.store.GetAnalysisCache(ctx, userID, store.CacheKindMeal, sha)
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
	if mealNegativeAboutLabel(res.Status) {
		s.dropNegativeCache(ctx, userID, store.CacheKindMeal, sha, res.Status)
		return nil, false
	}
	return &res, true
}

// cacheMealResult сохраняет разбор еды. Как и у сканера, статус error не кэшируется,
// а сила ответа решает конфликт с уже лежащей записью. У калорий признак прямой —
// res.Escalated: разбор целиком выполнила сильная модель, частичной подмены, как у
// сканера, здесь нет.
func (s *Server) cacheMealResult(ctx context.Context, userID int64, sha string, res *meal.Result) {
	if mealNegativeAboutLabel(res.Status) {
		return // отказ «упаковка без читаемого состава» не замораживаем
	}
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
