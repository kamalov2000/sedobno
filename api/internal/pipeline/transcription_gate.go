package pipeline

import (
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/sostav/api/internal/llm"
)

// Детерминированный антигаллюцинационный гейт. Confidence-порог (lowConfidence) ловит
// только НЕУВЕРЕННЫЕ промахи — а модель может уверенно выдумать правдоподобный состав
// «типового продукта категории» (разбор beta-скана #34, «Слон в чайном лесу»: кармин и
// сорбат калия, которых на этикетке не было, при высокой self-reported confidence).
// Confidence такое поймать не может по конструкции — модель не знает, что галлюцинирует.
//
// Гейт сверяет КАЖДЫЙ извлечённый ингредиент/число с transcription — дословной перепиской
// блока «Состав», которую модель обязана сделать ДО списка (extract_v1.txt). Выдумать
// согласованную пару «transcription + список» тоже можно, но это ощутимо труднее, чем
// выдумать один список — а несовпадение на штрафном ингредиенте дополнительно защищено
// эскалацией на сильную модель.

// normalizeForMatch готовит текст к нечёткому сравнению: нижний регистр, ё→е, все
// небуквенно-нечисловые разделители (дефисы, скобки, пунктуация) схлопываются в один
// пробел. Одна и та же функция для transcription и для проверяемого текста — иначе
// несовпадение форматирования (дефис/скобка/регистр) давало бы ложные эскалации.
func normalizeForMatch(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "ё", "е")
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := true
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevSpace = false
		} else if !prevSpace {
			b.WriteByte(' ')
			prevSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

// transcriptionMismatch проверяет каждую запись фазы 2 на нечёткое вхождение её сырого
// текста в transcription. Не найдено:
//   - штрафной (matched && risk>0, реально способен дать вычет) — кандидат на эскалацию:
//     цена ошибочного вычета за выдумку выше цены лишнего вызова сильной модели;
//   - нештрафной (risk=0 или не сматчен) — просто ненадёжен, вычесть за него всё равно
//     нечего, эскалация не нужна.
//
// Пустая transcription (старый провайдер/мок без этого поля, либо честное «состава нет
// вовсе») гейт полностью пропускает — сверяться не с чем, ложных эскалаций быть не должно.
func transcriptionMismatch(recs []rec, transcription string) (risky []rec, unreliable []int) {
	haystack := normalizeForMatch(transcription)
	if haystack == "" {
		return nil, nil
	}
	for i, r := range recs {
		needle := normalizeForMatch(r.raw)
		if needle == "" || strings.Contains(haystack, needle) {
			continue
		}
		if r.matched && r.risk > 0 {
			risky = append(risky, r)
		} else {
			unreliable = append(unreliable, i)
		}
	}
	return risky, unreliable
}

// markUnreliable снимает словарные данные с записей по индексам idx: раз сырой текст не
// нашёлся в transcription, ингредиенту нельзя доверять — показываем его как нейтральную
// несматченную строку (в скоринг такая и так не идёт, но и словарные пояснения к
// непроверенному тексту показывать нечестно).
func markUnreliable(recs []rec, idx []int) []rec {
	for _, i := range idx {
		recs[i].matched = false
		recs[i].code = ""
		recs[i].name = ""
		recs[i].category = ""
		recs[i].risk = 0
		recs[i].verdictShort = ""
		recs[i].verdictFull = ""
		recs[i].sourcesNote = ""
	}
	return recs
}

// nutritionKeywords — признаки того, что transcription (задуманная как переписка ТОЛЬКО
// блока «Состав», extract_v1.txt) всё же содержит блок пищевой ценности целиком: на
// некоторых этикетках оба блока напечатаны вплотную и попадают в один текст. Только тогда
// есть с чем сверять числа. Фразы ОБЯЗАНЫ быть характерны именно для заголовка таблицы —
// одиночные корни «жир»/«белк»/«углевод» НЕ годятся: это обычные слова состава («молочный
// жир», «яичный белок», «жирность 3.2%»), из-за них гейт ложно решал, что нутриент-блок
// есть, и молча прятал верно прочитанные числа (живой прогон evalpipe на 5 фото прода,
// 2026-07-23). "ккал"/"kcal" оставлены — как самостоятельное слово встречаются практически
// только в таблице КБЖУ, не в перечне ингредиентов.
var nutritionKeywords = []string{
	"пищевая ценность", "энергетическая ценность", "ккал", "kcal", "калорийность",
}

func transcriptionHasNutritionBlock(transcription string) bool {
	low := strings.ToLower(transcription)
	for _, kw := range nutritionKeywords {
		if strings.Contains(low, kw) {
			return true
		}
	}
	return false
}

// extractNumbers достаёт из текста все числа: последовательности цифр, где запятая или
// точка МЕЖДУ цифрами читается как десятичный разделитель (как на этикетках, «32,48»).
func extractNumbers(s string) []float64 {
	var out []float64
	runes := []rune(s)
	for i := 0; i < len(runes); {
		if !unicode.IsDigit(runes[i]) {
			i++
			continue
		}
		start := i
		for i < len(runes) && unicode.IsDigit(runes[i]) {
			i++
		}
		if i < len(runes) && (runes[i] == ',' || runes[i] == '.') && i+1 < len(runes) && unicode.IsDigit(runes[i+1]) {
			i++
			for i < len(runes) && unicode.IsDigit(runes[i]) {
				i++
			}
		}
		numStr := strings.ReplaceAll(string(runes[start:i]), ",", ".")
		if v, err := strconv.ParseFloat(numStr, 64); err == nil {
			out = append(out, v)
		}
	}
	return out
}

// numberPresent — встречается ли v среди чисел transcription, с допуском 0.5 (этикетки и
// модель округляют по-разному — «32,48» ккал может уйти в текст как «32,5»).
func numberPresent(v float64, numbers []float64) bool {
	for _, n := range numbers {
		if math.Abs(n-v) < 0.5 {
			return true
		}
	}
	return false
}

// nutritionMismatch — КБЖУ-числа модели должны буквально встречаться в transcription, НО
// только когда та реально содержит блок пищевой ценности (обычно он на ДРУГОЙ панели
// этикетки и в transcription не попадает вовсе — тогда сверяться не с чем, гейт молчит).
// Возвращает имена полей, которых не нашлось (пусто — либо сверять не с чем, либо все на
// месте). Отдельной эскалации на несовпадение КБЖУ нет (в отличие от ингредиентов) —
// балл эти числа не считает, они только для отображения; несовпадение просто прячется.
func nutritionMismatch(n *llm.Nutrition, transcription string) []string {
	if n == nil || !transcriptionHasNutritionBlock(transcription) {
		return nil
	}
	numbers := extractNumbers(transcription)
	var missing []string
	check := func(name string, v *float64) {
		if v != nil && !numberPresent(*v, numbers) {
			missing = append(missing, name)
		}
	}
	check("kcal", n.Kcal)
	check("protein_g", n.ProteinG)
	check("fat_g", n.FatG)
	check("carb_g", n.CarbG)
	check("net_amount", n.NetAmount)
	return missing
}
