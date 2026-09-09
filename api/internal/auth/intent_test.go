package auth

import "testing"

// Диплинк несёт две разные вещи: откуда человек пришёл и что он хотел сделать. Их
// обязательно разделять — иначе «mode-meal» осядет в acquisition_source и в отчёте по
// размещениям появится канал, которого не существует.
func TestParseStartIntent(t *testing.T) {
	cases := []struct {
		payload            string
		source, mode, plan string
	}{
		// Старые ссылки кампаний работают как работали.
		{"tg_ovz", "tg_ovz", "", ""},
		{"", "", "", ""},
		// Чистое намерение — источника нет.
		{"mode-meal", "", "meal", ""},
		{"mode-label", "", "scan", ""},
		{"plan-year", "", "", "annual"},
		{"plan-month", "", "", "monthly"},
		// И то и другое сразу: клик по кнопке «Проверить этикетку» в посте канала.
		{"tg_ovz__mode-label", "tg_ovz", "scan", ""},
		{"tg_ovz__plan-year", "tg_ovz", "", "annual"},
		{"mode-meal__tg_ovz", "tg_ovz", "meal", ""},
		// Чужой алфавит убивает payload ЦЕЛИКОМ (SanitizeStartParam проверяет всю
		// строку, а не по сегментам). Так безопаснее: из «mode-нечто» не появится
		// полуразобранный источник «mode-», которого никто не задавал.
		{"mode-нечто", "", "", ""},
		{"tg_ovz__mode-meal; drop", "", "", ""},
	}
	for _, c := range cases {
		got := ParseStartIntent(c.payload)
		if got.Source != c.source || got.Mode != c.mode || got.Plan != c.plan {
			t.Errorf("ParseStartIntent(%q) = {%q %q %q}, ждали {%q %q %q}",
				c.payload, got.Source, got.Mode, got.Plan, c.source, c.mode, c.plan)
		}
	}
}
