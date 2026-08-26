package server

import "testing"

// Канал посева из диплинка. t.me/бот?start=tg_ovz доезжает до бота обычным сообщением
// «/start tg_ovz» — если его не разобрать, атрибуция размещения теряется у всех, кто
// не дошёл до mini-app (а до неё доходят не все).
func TestStartPayload(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		// Ради чего всё: боевой диплинк канала.
		{"/start tg_ovz", "tg_ovz"},
		{"  /start   tg_ovz  ", "tg_ovz"},
		{"/start@sedobno_bot tg_ovz", "tg_ovz"},
		{"/start channel-123_ab", "channel-123_ab"},

		// Канала нет — пусто, и апсерт (COALESCE) существующий источник не затрёт.
		{"/start", ""},
		{"   /start   ", ""},
		{"/start@sedobno_bot", ""},
		{"", ""},

		// Не наша команда: «/start» лишь префикс другого слова.
		{"/started", ""},
		{"/startup tg_ovz", ""},

		// Санитайзинг: алфавит Telegram [A-Za-z0-9_-]. Что не прошло — пусто, а не «как
		// есть»: мусорная метка в отчёте по посевам хуже отсутствующей.
		{"/start пусть_кириллица", ""},
		{"/start tg ovz", ""},          // пробел внутри — два слова, метка не цельная
		{"/start tg;drop table", ""},   // спецсимволы
		{"/start ../../etc/passwd", ""},
		{"/start <script>", ""},
	}
	for _, tt := range tests {
		if got := startPayload(tt.text); got != tt.want {
			t.Errorf("startPayload(%q) = %q, ждали %q", tt.text, got, tt.want)
		}
	}
}

// Длина: Telegram и так режет payload 64 символами, но входу не доверяем.
func TestStartPayload_Длина(t *testing.T) {
	ok64 := ""
	for range 64 {
		ok64 += "a"
	}
	if got := startPayload("/start " + ok64); got != ok64 {
		t.Errorf("64 символа должны проходить целиком, получили %d", len(got))
	}
	if got := startPayload("/start " + ok64 + "XXXX"); len(got) != 64 {
		t.Errorf("длиннее потолка режется до 64, получили %d", len(got))
	}
}
