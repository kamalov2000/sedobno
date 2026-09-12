package meal

import (
	"testing"

	"github.com/sostav/api/internal/llm"
)

// Каша с предположенной ложкой сливочного масла: база 270 ккал, масло — 37 ккал и 4,1 г жира.
func porridgeWithButter() []llm.MealItem {
	return []llm.MealItem{{
		Name: "каша овсяная", Grams: 250, Kcal: 270, ProteinG: 8, FatG: 6, CarbG: 45, Confidence: 0.8,
		Pieces: []llm.MealPiece{{Kind: kindButter, Count: 1, Assumed: true}},
	}}
}

// Наследный приём узнаётся ТОЛЬКО по сохранённому итогу: одни и те же блюда с итогом 307
// (добавка внутри) и 270 (добавка снаружи) — это разные случаи, и второй трогать нельзя.
func TestНаследныйПриёмУзнаётсяПоСохранённомуИтогу(t *testing.T) {
	cases := []struct {
		имя    string
		stored llm.MealMacros
		ждём   int
	}{
		{"итог включает добавку — наследный", llm.MealMacros{Kcal: 307, ProteinG: 8, FatG: 10.1, CarbG: 45}, 1},
		{"итог уже без добавки (сохранён после релиза)", llm.MealMacros{Kcal: 270, ProteinG: 8, FatG: 6, CarbG: 45}, 0},
		{"числа не сходятся ни с одной версией — не выдумываем", llm.MealMacros{Kcal: 500, ProteinG: 8, FatG: 10.1, CarbG: 45}, 0},
		{"ккал сошлись, а жиры нет — тоже не наследный", llm.MealMacros{Kcal: 307, ProteinG: 8, FatG: 6, CarbG: 45}, 0},
	}
	for _, c := range cases {
		got := AssumedInTotal(porridgeWithButter(), c.stored)
		if len(got) != c.ждём {
			t.Errorf("%s: добавок %d, ждали %d", c.имя, len(got), c.ждём)
		}
	}
}

// Добавку называем человеку: вид, подпись, количество и цена вопроса в ккал — без них
// вопрос «оставить или удалить?» задавать не о чем.
func TestНаследнаяДобавкаНазванаЦеликом(t *testing.T) {
	got := AssumedInTotal(porridgeWithButter(), llm.MealMacros{Kcal: 307, ProteinG: 8, FatG: 10.1, CarbG: 45})
	if len(got) != 1 {
		t.Fatalf("добавок %d, ждали 1", len(got))
	}
	if got[0].Kind != kindButter || got[0].Label != "Сливочное" || got[0].Count != 1 || got[0].Kcal != 37 {
		t.Errorf("добавка описана неверно: %+v", got[0])
	}
}

// Приём без предположенного и приём с ПОДТВЕРЖДЁННОЙ добавкой наследными не считаются:
// первому нечего терять, у второго добавка и так в итоге на законных основаниях.
func TestОбычныйПриёмНеНаследный(t *testing.T) {
	plain := []llm.MealItem{{Name: "каша овсяная", Grams: 250, Kcal: 270, ProteinG: 8, FatG: 6, CarbG: 45}}
	if got := AssumedInTotal(plain, llm.MealMacros{Kcal: 270, ProteinG: 8, FatG: 6, CarbG: 45}); len(got) != 0 {
		t.Errorf("приём без добавок: %+v", got)
	}
	confirmed := porridgeWithButter()
	confirmed[0].Pieces[0].Assumed = false
	if got := AssumedInTotal(confirmed, llm.MealMacros{Kcal: 307, ProteinG: 8, FatG: 10.1, CarbG: 45}); len(got) != 0 {
		t.Errorf("подтверждённая добавка: %+v", got)
	}
}

// Три решения — три исхода. Пустое решение (старый клиент, закрытый диалог) обязано вести
// себя как «пока оставить»: молча уменьшать сохранённое число нельзя.
func TestРешениеПоНаследнымДобавкам(t *testing.T) {
	cases := []struct {
		решение     string
		ккал        float64
		жиры        float64
		кусков      int
		ещёПредполо bool
	}{
		{AssumedKeep, 307, 10.1, 1, false},
		{AssumedRemove, 270, 6, 0, false},
		{"", 307, 10.1, 1, true},
	}
	for _, c := range cases {
		dishes, total := ApplyAssumedDecision(porridgeWithButter(), c.решение)
		if total.Kcal != c.ккал || total.FatG != c.жиры {
			t.Errorf("решение %q: итог %v ккал / %v жира, ждали %v / %v", c.решение, total.Kcal, total.FatG, c.ккал, c.жиры)
		}
		if len(dishes[0].Pieces) != c.кусков {
			t.Errorf("решение %q: кусков %d, ждали %d", c.решение, len(dishes[0].Pieces), c.кусков)
		}
		if c.кусков > 0 && dishes[0].Pieces[0].Assumed != c.ещёПредполо {
			t.Errorf("решение %q: assumed=%v, ждали %v", c.решение, dishes[0].Pieces[0].Assumed, c.ещёПредполо)
		}
	}
}

// Решение не портит исходные блюда вызывающего: обработчик отдаёт в него уже
// санитизированный срез и пишет в базу ТОТ, что вернули, — подмена на месте дала бы
// «удалили» там, где человек ничего не решал.
func TestРешениеНеПравитИсходныеБлюда(t *testing.T) {
	in := porridgeWithButter()
	ApplyAssumedDecision(in, AssumedRemove)
	if len(in[0].Pieces) != 1 || !in[0].Pieces[0].Assumed {
		t.Errorf("исходные блюда изменились: %+v", in[0].Pieces)
	}
}

// Несколько предположенных добавок — все в итоге и все названы.
func TestНесколькоНаследныхДобавок(t *testing.T) {
	dishes := []llm.MealItem{{
		Name: "каша овсяная", Grams: 250, Kcal: 270, ProteinG: 8, FatG: 6, CarbG: 45,
		Pieces: []llm.MealPiece{
			{Kind: kindButter, Count: 1, Assumed: true},
			{Kind: kindSugar, Count: 2, Assumed: true},
		},
	}}
	stored := llm.MealMacros{Kcal: 347, ProteinG: 8, FatG: 10.1, CarbG: 55}
	got := AssumedInTotal(dishes, stored)
	if len(got) != 2 {
		t.Fatalf("добавок %d, ждали 2", len(got))
	}
	if got[1].Kcal != 40 || got[1].Count != 2 {
		t.Errorf("сахар описан неверно: %+v", got[1])
	}
	_, total := ApplyAssumedDecision(dishes, "")
	if total.Kcal != 347 || total.CarbG != 55 {
		t.Errorf("без решения итог %v ккал / %v углеводов, ждали 347 / 55", total.Kcal, total.CarbG)
	}
	_, total = ApplyAssumedDecision(dishes, AssumedRemove)
	if total.Kcal != 270 || total.CarbG != 45 {
		t.Errorf("после «удалить все» итог %v ккал / %v углеводов, ждали 270 / 45", total.Kcal, total.CarbG)
	}
}

func TestРешениеВалидируется(t *testing.T) {
	for _, v := range []string{"", AssumedKeep, AssumedRemove} {
		if !ValidAssumedDecision(v) {
			t.Errorf("решение %q должно приниматься", v)
		}
	}
	for _, v := range []string{"KEEP", "оставить", "drop", "true"} {
		if ValidAssumedDecision(v) {
			t.Errorf("решение %q принимать нельзя", v)
		}
	}
}
