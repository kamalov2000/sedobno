package pipeline

import (
	"testing"

	"github.com/sostav/api/internal/llm"
)

func TestNormalizeForMatch(t *testing.T) {
	cases := map[string]string{
		"Консервант (Сорбат калия)": "консервант сорбат калия",
		"сорбат-калия":              "сорбат калия",
		"Ё-моё":                     "е мое",
		"  двойные   пробелы  ":     "двойные пробелы",
		"":                          "",
	}
	for in, want := range cases {
		if got := normalizeForMatch(in); got != want {
			t.Errorf("normalizeForMatch(%q)=%q, ждали %q", in, got, want)
		}
	}
}

func TestTranscriptionMismatch_РискованныйКандидатНаЭскалацию(t *testing.T) {
	// Эталонный кейс (beta-скан #34, «Слон в чайном лесу»): модель дописала кармин
	// (E120, risk>0) и сахар, которых в дословной переписке нет.
	transcription := "вода, сок земляники, жасминовый чай, лимонная кислота, ароматизатор, витамин C"
	recs := []rec{
		{raw: "вода", matched: false},
		{raw: "сахар", matched: true, risk: 1, code: "sugar"},             // штрафной, не в transcription
		{raw: "лимонная кислота", matched: true, risk: 0, code: "E330"},   // есть в тексте
		{raw: "краситель (кармин)", matched: true, risk: 1, code: "E120"}, // штрафной, не в transcription
		{raw: "витамин C", matched: false},
	}
	risky, unreliable := transcriptionMismatch(recs, transcription)
	if len(risky) != 2 {
		t.Fatalf("risky=%d, ждали 2 (сахар, кармин): %+v", len(risky), risky)
	}
	if len(unreliable) != 0 {
		t.Errorf("unreliable=%v, ждали 0 (все несовпадения — штрафные)", unreliable)
	}
}

func TestTranscriptionMismatch_НештрафнойПометкаБезЭскалации(t *testing.T) {
	transcription := "молоко, сливки, сахар"
	recs := []rec{
		{raw: "молоко", matched: false},
		{raw: "натуральный ароматизатор", matched: false}, // не в тексте, но risk=0 — не штрафной
	}
	risky, unreliable := transcriptionMismatch(recs, transcription)
	if len(risky) != 0 {
		t.Fatalf("risky=%+v, ждали пусто (несовпадение только на нештрафном)", risky)
	}
	if len(unreliable) != 1 || unreliable[0] != 1 {
		t.Fatalf("unreliable=%v, ждали [1]", unreliable)
	}
}

func TestTranscriptionMismatch_ПустаяTranscriptionГейтМолчит(t *testing.T) {
	recs := []rec{{raw: "кармин", matched: true, risk: 1}}
	risky, unreliable := transcriptionMismatch(recs, "")
	if len(risky) != 0 || len(unreliable) != 0 {
		t.Fatalf("пустая transcription должна пропускать гейт целиком, получили risky=%v unreliable=%v", risky, unreliable)
	}
}

func TestTranscriptionMismatch_ЛегитимныйДвухсекционныйСоставНеРегрессирует(t *testing.T) {
	// Сканы прода #57/#29: этикетка честно содержит ДВЕ секции «Состав» (вафля в глазури,
	// картошка+соус) — обе секции попадают в transcription (правило extract_v1.txt),
	// поэтому дублирующиеся ингредиенты («сахар» дважды) находятся в тексте и не мешают.
	transcription := "сахар, какао-масло, сухое молоко, пшеничная мука. сахар, пшеничная мука, растительное масло, соль"
	recs := []rec{
		{raw: "сахар", matched: true, risk: 1, code: "sugar"},
		{raw: "какао-масло", matched: false},
		{raw: "сухое молоко", matched: false},
		{raw: "пшеничная мука", matched: false},
		{raw: "сахар", matched: true, risk: 1, code: "sugar"}, // вторая секция, та же строка
		{raw: "пшеничная мука", matched: false},
		{raw: "растительное масло", matched: false},
		{raw: "соль", matched: true, risk: 1, code: "salt"},
	}
	risky, unreliable := transcriptionMismatch(recs, transcription)
	if len(risky) != 0 || len(unreliable) != 0 {
		t.Fatalf("двухсекционный состав дал ложное несовпадение: risky=%v unreliable=%v", risky, unreliable)
	}
}

func TestMarkUnreliable_СнимаетСловарныеДанные(t *testing.T) {
	recs := []rec{{raw: "x", matched: true, code: "E100", risk: 2, name: "жёлтый"}}
	recs = markUnreliable(recs, []int{0})
	r := recs[0]
	if r.matched || r.code != "" || r.risk != 0 || r.name != "" {
		t.Errorf("markUnreliable не очистил запись: %+v", r)
	}
	if r.raw != "x" {
		t.Errorf("raw не должен меняться: %q", r.raw)
	}
}

func f64(v float64) *float64 { return &v }

func TestNutritionMismatch_ЧиселНетВБлокеПищевойЦенности(t *testing.T) {
	// Эталон: заявлено 48 ккал/12 У, а transcription (содержит блок КБЖУ) говорит про
	// другие числа — 32,48 ккал / 8,0 У.
	transcription := "пищевая ценность на 100 мл: белки 0, жиры 0, углеводы 8,0, калорийность 32,48 ккал"
	n := &llm.Nutrition{Present: true, Kcal: f64(48), CarbG: f64(12)}
	missing := nutritionMismatch(n, transcription)
	if len(missing) != 2 {
		t.Fatalf("missing=%v, ждали 2 поля (kcal, carb_g)", missing)
	}
}

func TestNutritionMismatch_ЧислаСовпадаютМолчит(t *testing.T) {
	transcription := "энергетическая ценность 100 г: 121 ккал, белки 17.2, жиры 5, углеводы 1.8"
	n := &llm.Nutrition{Present: true, Kcal: f64(121), ProteinG: f64(17.2), FatG: f64(5), CarbG: f64(1.8)}
	if missing := nutritionMismatch(n, transcription); len(missing) != 0 {
		t.Errorf("missing=%v, ждали пусто — все числа на месте", missing)
	}
}

func TestNutritionMismatch_НетБлокаПищевойЦенностиГейтМолчит(t *testing.T) {
	// transcription — только состав, без слов про КБЖУ: сверять не с чем, гейт не должен
	// придираться (nutrition обычно на ДРУГОЙ панели этикетки).
	transcription := "вода, сахар, лимонная кислота"
	n := &llm.Nutrition{Present: true, Kcal: f64(999)}
	if missing := nutritionMismatch(n, transcription); len(missing) != 0 {
		t.Errorf("missing=%v, ждали пусто (transcription не содержит блок КБЖУ)", missing)
	}
}

// Регрессия живого прогона (evalpipe, 5 фото прода, 2026-07-23): «молочный жир»/«яичный
// белок»/«жирность 3.2%» — обычные слова СОСТАВА, не признак таблицы КБЖУ. Со старым
// (слишком широким) списком ключевых слов гейт ложно решал, что нутриент-блок есть, и
// молча прятал верно прочитанные числа на каждом втором молочном продукте.
func TestNutritionMismatch_ОбычныеСловаСостава_НеЛожноеСрабатывание(t *testing.T) {
	cases := []string{
		"целое молоко, обезжиренное молоко, молочный жир, сахар",
		"яичный белок, яичный желток, соль",
		"молоко нормализованное, жирность 3.2%, закваска",
	}
	for _, transcription := range cases {
		n := &llm.Nutrition{Present: true, Kcal: f64(330), ProteinG: f64(7.5), FatG: f64(8.5), CarbG: f64(55)}
		if missing := nutritionMismatch(n, transcription); len(missing) != 0 {
			t.Errorf("transcription=%q: missing=%v, ждали пусто — это состав, не таблица КБЖУ", transcription, missing)
		}
	}
}

func TestExtractNumbers(t *testing.T) {
	got := extractNumbers("32,48 ккал, объём 520 мл, доля 8.0%")
	want := []float64{32.48, 520, 8.0}
	if len(got) != len(want) {
		t.Fatalf("extractNumbers=%v, ждали %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("got[%d]=%v, ждали %v", i, got[i], v)
		}
	}
}
