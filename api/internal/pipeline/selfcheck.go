package pipeline

import (
	"context"
	"sync"

	"github.com/sostav/api/internal/llm"
	"github.com/sostav/api/internal/scoring"
)

// Прицельный self-consistency: второе прочтение ТОГО ЖЕ кадра сильной моделью — но
// только там, где детекторы плотности уже сказали «этикетка мелкая» (форс-эскалация).
//
// Зачем. Форс-эскалация убрала галлюцинации дешёвой модели, но не убрала разброс — он
// переехал на сильную. Один кадр онигири, четыре ВЕРНЫХ (не выдуманных) чтения Sonnet:
// 38 / 79 / 48 / 68. Составы правдоподобны, но модель то дочитывает подсластители соуса
// (аспартам, сахарин), то нет, и по-разному раскрывает вложенные скобки. Кэш замораживает
// ОДИН из этих ответов — пользователь видит стабильное число, но какое именно, решает
// жребий. Второй прогон превращает жребий в честное предупреждение: там, где модель на
// пределе, мы об этом говорим, а не молчим одним числом.
//
// Почему прицельно, а не на каждом скане: сплошной второй прогон стоит +129 % к цене
// скана, прицельный (те же ~12 % флагнутых кадров) — +34 % за тот же сигнал ровно там,
// где он нужен.
//
// Почему два вызова дают РАЗНЫЕ ответы, хотя вход один. У Sonnet 5 параметр temperature
// удалён (llm/anthropic.go, temperatureFor): моделью сэмплинг управляется сама, и
// детерминизма на плотном кадре нет — тот самый разброс 38…79 и есть предмет замера.
// Была бы модель детерминированной, второй вызов не нёс бы информации, и всю эту
// механику пришлось бы строить иначе (разные ракурсы/кропы, а не повтор).
//
// Латентность НЕ растёт: оба вызова идут параллельно (extractPair), и обе нормализации
// фазы 2 — тоже (normalizePair). Последовательный вариант превратил бы флагнутый скан
// из ~23 с в ~46 с, что неприемлемо.

// pairExtraction — исход одного из двух параллельных прочтений.
type pairExtraction struct {
	ext   *llm.Extraction
	usage llm.Usage
	err   error
}

// extractPair делает ДВА параллельных вызова фазы 1 одной и той же моделью по одному и
// тому же кадру. Первый — основной (его ответ увидит пользователь), второй — контрольный.
//
// Шлюз допуска (§15) сохраняется ровно как у обычного входа: слот основного вызова
// берётся НЕблокирующим tryAcquire, нет слота — ErrBusy, работа не начиналась. Слот
// контрольного вызова берётся так же неблокирующе, и его отсутствие НЕ ошибка: под
// нагрузкой self-consistency отключается первым и сам собой, а скан отрабатывает как
// обычная форс-эскалация. Это осознанное свойство: перепроверка — роскошь, доступ
// пользователя к ответу — нет.
// Возвращаемый usage — сумма ОБОИХ вызовов, независимо от того, чей ответ стал основным
// и не отбросили ли мы один из них: заплачено за оба, и учёт стоимости обязан это видеть.
func (p *Pipeline) extractPair(ctx context.Context, prov llm.LLMProvider, images []llm.Image) (primary, control pairExtraction, usage llm.Usage, err error) {
	if !p.tryAcquire() {
		return primary, control, usage, ErrBusy
	}
	hasControl := p.tryAcquire()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer p.release()
		primary.ext, primary.usage, primary.err = prov.ExtractIngredients(ctx, images)
	}()
	if hasControl {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer p.release()
			control.ext, control.usage, control.err = prov.ExtractIngredients(ctx, images)
		}()
	}
	wg.Wait()

	if !hasControl {
		p.log.Info("self-consistency: нет свободного слота под контрольный вызов, идём обычной форс-эскалацией")
	}
	usage = primary.usage.Add(control.usage)
	primary, control, err = p.choosePrimary(primary, control)
	return primary, control, usage, err
}

// choosePrimary решает, чей из двух ответов станет ОТВЕТОМ ПОЛЬЗОВАТЕЛЮ. Два вызова
// одинаковы по построению (та же модель, тот же кадр, тот же промпт), поэтому «первый»
// и «контрольный» — не свойства ответов, а имена переменных. Выбирать между двумя
// прочитанными составами мы по-прежнему не беремся: правило срабатывает только там, где
// выбора по существу нет.
//
//   - вызов сорвался (сеть, таймаут), а второй дошёл — отвечает дошедший. Ронять скан,
//     держа в руках оплаченный годный ответ, незачем; второго мнения при этом нет, и
//     оговорки «приблизительно» тоже (сбой сети — не разногласие модели с собой);
//   - один увидел состав, другой не увидел ничего — отвечает увидевший. Пустое прочтение
//     не содержит информации, которой нет у непустого, а «не вижу состав» вместо готового
//     разбора было бы худшим из двух исходов. Вторым мнением оно при этом остаётся, и
//     расхождение (штрафные добавки против пустого множества) честно даст «приблизительно».
func (p *Pipeline) choosePrimary(primary, control pairExtraction) (pairExtraction, pairExtraction, error) {
	if primary.err != nil {
		if usableControl(control) {
			p.log.Warn("self-consistency: основной вызов сорвался, отвечаем контрольным прочтением", "err", primary.err)
			return control, pairExtraction{}, nil
		}
		return primary, pairExtraction{}, nil // оба не дошли — разбираться будет вызывающий
	}
	if !usableControl(control) {
		return primary, control, nil
	}
	if !sawComposition(primary.ext) && sawComposition(control.ext) {
		p.log.Info("self-consistency: основное прочтение состава не увидело, отвечаем контрольным")
		return control, primary, nil
	}
	return primary, control, nil
}

// sawComposition — прочтение принесло состав, с которым есть что делать. Тот же
// предикат, которым пайплайн уже принимает результат эскалации по гейту transcription.
func sawComposition(e *llm.Extraction) bool {
	return e != nil && e.IsLabel && len(e.Ingredients) > 0
}

// normalizePair прогоняет фазу 2 для обоих прочтений ПАРАЛЛЕЛЬНО. Оба списка обязаны
// пройти ОДИН И ТОТ ЖЕ путь нормализации, включая LLM-добор по несматченным строкам:
// сравнивать «со словарём и добором» против «только со словарём» нельзя — асимметрия
// сама породила бы расхождения там, где модель прочитала одинаково.
//
// Ошибка добора у любой из сторон не фатальна (как и в одиночном пути): часть строк
// останется несматченной, обе стороны продолжают.
func (p *Pipeline) normalizePair(ctx context.Context, primary, control []string) (recsA []rec, usageA llm.Usage, errA error, recsB []rec, usageB llm.Usage) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		recsA, usageA, errA = p.normalize(ctx, primary)
	}()
	go func() {
		defer wg.Done()
		var errB error
		recsB, usageB, errB = p.normalize(ctx, control)
		if errB != nil {
			p.log.Warn("self-consistency: фаза 2 контрольного прочтения — добор LLM не удался", "err", errB)
		}
	}()
	wg.Wait()
	return recsA, usageA, errA, recsB, usageB
}

// penalizedDiff — симметрическая разность двух множеств штрафных кодов (оба
// отсортированы, см. scoring.Result.Penalized). onlyPrimary — что нашло первое
// прочтение и не нашло второе, onlyControl — наоборот.
//
// Сравниваем именно НАБОР добавок, а не баллы — решение владельца, и оно честнее.
// Разница баллов смешивает два разных события: «модель прочитала другой состав»
// (пропущенный аспартам — 38 против 79) и «модель иначе расставила позиции» (сахар
// уехал с 1-й на 2-ю, штраф −15 против −5). Первое — настоящий предел чтения, второе —
// арифметика по одному и тому же прочитанному тексту. Порог в баллах пришлось бы
// выбирать наугад; у множества порога нет вовсе — оно либо совпало, либо нет.
func penalizedDiff(primary, control []string) (onlyPrimary, onlyControl []string) {
	inControl := make(map[string]struct{}, len(control))
	for _, c := range control {
		inControl[c] = struct{}{}
	}
	inPrimary := make(map[string]struct{}, len(primary))
	for _, c := range primary {
		inPrimary[c] = struct{}{}
		if _, ok := inControl[c]; !ok {
			onlyPrimary = append(onlyPrimary, c)
		}
	}
	for _, c := range control {
		if _, ok := inPrimary[c]; !ok {
			onlyControl = append(onlyControl, c)
		}
	}
	return onlyPrimary, onlyControl
}

// scoringItems переводит записи фазы 2 во вход детерминированного скоринга (фаза 3).
// Общая для основного ответа и для контрольного прочтения: сравнение имеет смысл
// только если обе стороны считаются ОДНИМ кодом.
func scoringItems(recs []rec) []scoring.Ingredient {
	items := make([]scoring.Ingredient, 0, len(recs))
	for _, r := range recs {
		items = append(items, scoring.Ingredient{
			Code: r.code, Category: r.category, Name: r.name, VerdictShort: r.verdictShort,
			VerdictFull: r.verdictFull, Risk: r.risk, Position: r.pos, Matched: r.matched,
			// Основание оценки добавки из словаря — доезжает до штрафа, чтобы по каждому
			// вычету было видно не только «сколько», но и «почему».
			Basis: ingredientBasis(r.sourcesNote),
		})
	}
	return items
}

// applySelfCheck сравнивает контрольное прочтение с основным и, если наборы штрафных
// добавок разошлись, помечает балл приблизительным.
//
// Победителя НЕ выбираем. Когда две одинаково сильные читки одного кадра расходятся,
// признака «какая из них верна» у нас нет — ни confidence (она у обеих высокая, в этом
// вся суть предела), ни длина списка (длиннее ≠ правдивее: дочитать соус и сочинить
// лишнее выглядят одинаково). Поэтому балл остаётся за первым прочтением, а расхождение
// превращается в предупреждение — это и есть честный выход, тот же по смыслу, что знак
// «≈» в калориях.
func (p *Pipeline) applySelfCheck(res *Result, primary scoring.Result, recsControl []rec) {
	control := p.scorer.Score(scoringItems(recsControl))
	onlyPrimary, onlyControl := penalizedDiff(primary.Penalized, control.Penalized)

	res.SelfCheckRan = true
	res.SelfCheckScore = control.Score
	res.SelfCheckOnlyA = onlyPrimary
	res.SelfCheckOnlyB = onlyControl

	if len(onlyPrimary) == 0 && len(onlyControl) == 0 {
		// Наборы совпали. Балл при этом мог разойтись (позиции), но это уже арифметика
		// по одному и тому же прочитанному составу — не повод сомневаться в чтении.
		p.log.Info("self-consistency: прочтения сошлись по набору штрафных добавок",
			"балл", primary.Score, "контроль", control.Score, "штрафных", len(primary.Penalized))
		return
	}
	res.ScoreApproximate = true
	p.log.Warn("self-consistency: прочтения разошлись, балл приблизителен",
		"балл", primary.Score, "контроль", control.Score,
		"только_в_первом", onlyPrimary, "только_в_контрольном", onlyControl)
}

// usableControl — годится ли контрольное прочтение для сравнения.
//
// Различаем два «нет ответа», и разница принципиальная:
//   - вызов НЕ СОСТОЯЛСЯ (ошибка сети, таймаут, нет слота) — второго мнения просто нет,
//     сравнивать не с чем, признак «приблизительно» не выставляем;
//   - вызов состоялся, но модель НЕ УВИДЕЛА состав (is_label=false, пустой список) —
//     это полноценное второе мнение, и оно противоречит первому. Его штрафное множество
//     пусто, и расхождение с непустым множеством первого прочтения — честный сигнал
//     «на этом кадре модель не воспроизводит саму себя».
func usableControl(c pairExtraction) bool {
	return c.err == nil && c.ext != nil
}
