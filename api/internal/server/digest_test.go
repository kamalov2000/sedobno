package server

import (
	"strings"
	"testing"
)

// ТОН — предмет отдельной проверки, а не вкусовщина: продукт про еду, часть аудитории —
// люди с непростыми отношениями с ней. Подгонять и стыдить нельзя.
func TestDigestText_NoPressure(t *testing.T) {
	texts := []string{
		digestText(1340, 3, 2100, 4, false),
		digestText(1340, 3, 0, 0, true),
		digestText(900, 1, 1800, 1, false),
	}
	// Формулировки-подгонялки: обратные отсчёты, «не потеряйте», «осталось до нормы»,
	// упрёки за незаписанное.
	banned := []string{"не потеряй", "потеряете", "не пропустите", "успей", "осталось", "сгорит", "продержитесь", "всего лишь", "маловато", "перебор"}
	for _, txt := range texts {
		low := strings.ToLower(txt)
		for _, b := range banned {
			if strings.Contains(low, b) {
				t.Errorf("в итоге дня есть подгоняющая формулировка %q:\n%s", b, txt)
			}
		}
	}
}

// Серия — такой же факт, как калории: сообщаем и не призываем беречь.
func TestDigestText_StreakIsAFact(t *testing.T) {
	txt := digestText(1340, 3, 0, 4, false)
	if !strings.Contains(txt, "4 дня подряд") {
		t.Fatalf("нет факта серии:\n%s", txt)
	}
	// Нулевую серию не упоминаем вовсе — «0 дней подряд» звучит как укор.
	if strings.Contains(digestText(1340, 3, 0, 0, false), "подряд") {
		t.Fatal("нулевая серия попала в текст")
	}
}

// Норма подставляется, только если она рассчитана.
func TestDigestText_TargetOptional(t *testing.T) {
	if !strings.Contains(digestText(1340, 3, 2100, 0, false), "1340 из 2100 ккал") {
		t.Fatal("норма не подставилась")
	}
	withoutTarget := digestText(1340, 3, 0, 0, false)
	if !strings.Contains(withoutTarget, "1340 ккал") || strings.Contains(withoutTarget, " из ") {
		t.Fatalf("без нормы текст неверен:\n%s", withoutTarget)
	}
}

// Как отключить — в ПЕРВОМ сообщении, дальше не повторяем.
func TestDigestText_OptOutOnlyInFirst(t *testing.T) {
	if !strings.Contains(digestText(1340, 3, 0, 0, true), "/stop_daily") {
		t.Fatal("в первом сообщении нет способа отключить")
	}
	if strings.Contains(digestText(1340, 3, 0, 0, false), "/stop_daily") {
		t.Fatal("приписка повторяется в последующих сообщениях")
	}
}

// Русские числительные: «1 приём», «2 приёма», «5 приёмов».
func TestDigestText_Plurals(t *testing.T) {
	cases := map[int]string{1: "1 приём", 2: "2 приёма", 5: "5 приёмов", 11: "11 приёмов", 21: "21 приём"}
	for n, want := range cases {
		if got := pluralMeals(n); got != want {
			t.Errorf("pluralMeals(%d)=%q, ждали %q", n, got, want)
		}
	}
	days := map[int]string{1: "1 день", 3: "3 дня", 7: "7 дней", 11: "11 дней", 22: "22 дня"}
	for n, want := range days {
		if got := pluralDays(n); got != want {
			t.Errorf("pluralDays(%d)=%q, ждали %q", n, got, want)
		}
	}
}

// Отписка обязана срабатывать от того, что человек напишет в ответ на рассылку
// естественно, — а не только от заученной команды.
func TestIsStopDigestCommand(t *testing.T) {
	yes := []string{"/stop_daily", "/stop_daily@sedobno_bot", "стоп", "Стоп", "СТОП!", "хватит", "отписаться", "stop", " стоп "}
	for _, s := range yes {
		if !isStopDigestCommand(s) {
			t.Errorf("%q не распознано как отписка", s)
		}
	}
	// Обращение в поддержку перехватывать нельзя.
	no := []string{"стоп, а сколько там калорий", "хватит ли мне 1500 ккал", "/start", "стопка", "остановись"}
	for _, s := range no {
		if isStopDigestCommand(s) {
			t.Errorf("%q ошибочно принято за отписку", s)
		}
	}
}
