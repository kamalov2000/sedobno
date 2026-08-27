package server

import (
	"strings"
	"testing"
)

// Тон напоминания — тот же запрет, что у вечернего итога (TestDigestText_NoPressure).
// Сообщение про деньги давит охотнее всего, поэтому проверяем его отдельно и строже:
// у человека заканчивается доступ, и соблазн «успей продлить» здесь максимальный.
func TestTrialReminderText_NoPressure(t *testing.T) {
	texts := []string{
		trialReminderText(12, 5, 249),
		trialReminderText(0, 0, 249),
		trialReminderText(1, 0, 249),
		trialReminderText(0, 3, 249),
	}
	banned := []string{
		"успей", "поспеши", "срочно", "не упусти", "не пропусти", "последний шанс",
		"сгорит", "сгорают", "потеряешь", "потеряете", "жаль", "только сегодня",
		"осталось всего", "не забудь",
	}
	for _, txt := range texts {
		low := strings.ToLower(txt)
		for _, b := range banned {
			if strings.Contains(low, b) {
				t.Errorf("в напоминании есть подгоняющая формулировка %q:\n%s", b, txt)
			}
		}
	}
}

// Главное содержание — ЕГО собственные числа за неделю: не обещание пользы, а уже
// полученная. И обещание, что записи никуда не денутся: без него «доступ заканчивается»
// читается как «заберут дневник».
func TestTrialReminderText_ЛичныеЧислаИСохранностьЗаписей(t *testing.T) {
	txt := trialReminderText(12, 5, 249)
	for _, want := range []string{"12 приёмов", "5 составов", "249 ₽"} {
		if !strings.Contains(txt, want) {
			t.Errorf("в напоминании нет «%s»:\n%s", want, txt)
		}
	}
	if !strings.Contains(strings.ToLower(txt), "дневник") {
		t.Errorf("не сказано, что дневник остаётся:\n%s", txt)
	}
}

// Русские числительные, а не «12 приём(ов)».
func TestTrialReminderText_Числительные(t *testing.T) {
	cases := map[int]string{1: "1 приём", 2: "2 приёма", 5: "5 приёмов", 11: "11 приёмов", 21: "21 приём"}
	for n, want := range cases {
		if txt := trialReminderText(n, 0, 249); !strings.Contains(txt, want) {
			t.Errorf("для %d ждали «%s»:\n%s", n, want, txt)
		}
	}
	scans := map[int]string{1: "1 состав", 3: "3 состава", 7: "7 составов"}
	for n, want := range scans {
		if txt := trialReminderText(0, n, 249); !strings.Contains(txt, want) {
			t.Errorf("для %d ждали «%s»:\n%s", n, want, txt)
		}
	}
}

// Кто за неделю ничего не сделал — про его «достижения» молчим: и хвалить не за что,
// и упрекать запрещено. Сообщение остаётся осмысленным.
func TestTrialReminderText_ПустаяНеделяБезУпрёка(t *testing.T) {
	txt := trialReminderText(0, 0, 249)
	if strings.Contains(txt, "0 ") {
		t.Errorf("нули вынесены в текст — это упрёк:\n%s", txt)
	}
	if !strings.Contains(txt, "неделя полного доступа") {
		t.Errorf("сообщение потеряло смысл:\n%s", txt)
	}
}
