package store

import (
	"context"
	"testing"
)

// readSource читает канал посева пользователя (NULL — пустая строка).
func readSource(t *testing.T, ctx context.Context, tgID int64) string {
	t.Helper()
	var v *string
	if err := testPool.QueryRow(ctx,
		`SELECT acquisition_source FROM users WHERE telegram_id = $1`, tgID).Scan(&v); err != nil {
		t.Fatalf("чтение acquisition_source: %v", err)
	}
	if v == nil {
		return ""
	}
	return *v
}

// Канал посева фиксируется ПЕРВЫМ касанием и дальше не меняется. Ровно на этом
// свойстве держится отчёт по размещениям: человек приходит по диплинку один раз, а
// заходит потом много, и каждый следующий заход не должен перекрашивать его источник.
func TestUpsertUser_КаналПосева(t *testing.T) {
	s, ctx := newStore(t)
	const tgID = 555000001

	// Первое касание по диплинку — источник записан.
	if _, err := s.UpsertUser(ctx, tgID, "vasya", "tg_ovz", 30, 30); err != nil {
		t.Fatalf("первый UpsertUser: %v", err)
	}
	if got := readSource(t, ctx, tgID); got != "tg_ovz" {
		t.Fatalf("после первого касания source=%q, ждали tg_ovz", got)
	}

	// Повторный заход БЕЗ источника (обычный /start, вход в mini-app) — не затирает.
	if _, err := s.UpsertUser(ctx, tgID, "vasya", "", 30, 30); err != nil {
		t.Fatalf("повторный UpsertUser: %v", err)
	}
	if got := readSource(t, ctx, tgID); got != "tg_ovz" {
		t.Errorf("пустой источник затёр канал: source=%q, ждали tg_ovz", got)
	}

	// Приход по ДРУГОМУ диплинку — тоже не перебивает: первый канал и привёл человека.
	if _, err := s.UpsertUser(ctx, tgID, "vasya", "tg_other", 30, 30); err != nil {
		t.Fatalf("UpsertUser с другим каналом: %v", err)
	}
	if got := readSource(t, ctx, tgID); got != "tg_ovz" {
		t.Errorf("второй канал перебил первый: source=%q, ждали tg_ovz", got)
	}
}

// Пришёл без диплинка, а канал доехал позже (бот не знал метки, mini-app принесла её в
// start_param) — источник дописывается: NULL перезаписывать МОЖНО, это не «затирание».
// Без этого свойства всякий, кто нажал /start до правки бота, остался бы без атрибуции.
func TestUpsertUser_КаналДописываетсяКПустому(t *testing.T) {
	s, ctx := newStore(t)
	const tgID = 555000002

	if _, err := s.UpsertUser(ctx, tgID, "petya", "", 30, 30); err != nil {
		t.Fatalf("первый UpsertUser: %v", err)
	}
	if got := readSource(t, ctx, tgID); got != "" {
		t.Fatalf("источник взялся из ниоткуда: %q", got)
	}
	if _, err := s.UpsertUser(ctx, tgID, "petya", "tg_ovz", 30, 30); err != nil {
		t.Fatalf("второй UpsertUser: %v", err)
	}
	if got := readSource(t, ctx, tgID); got != "tg_ovz" {
		t.Errorf("канал не дописался к пустому: source=%q, ждали tg_ovz", got)
	}
}

// Потолки триала принадлежат ЧЕЛОВЕКУ, а не настройке (миграция 0023). Повторный вход
// с другими настройками не должен переписывать уже обещанные условия: иначе снижение
// потолка ради экономики закрыло бы доступ тем, кто уже внутри своей недели.
func TestUpsertUser_ПотолкиТриалаНеПерезаписываются(t *testing.T) {
	s, ctx := newStore(t)

	u, err := s.UpsertUser(ctx, 8801, "lim", "tg_ovz", 30, 30)
	if err != nil {
		t.Fatalf("первый вход: %v", err)
	}
	if u.TrialMealLimit != 30 || u.TrialScanLimit != 30 {
		t.Fatalf("новому проставлено %d/%d, ждали 30/30", u.TrialMealLimit, u.TrialScanLimit)
	}

	again, err := s.UpsertUser(ctx, 8801, "lim", "", 10, 10)
	if err != nil {
		t.Fatalf("повторный вход: %v", err)
	}
	if again.TrialMealLimit != 30 || again.TrialScanLimit != 30 {
		t.Errorf("условия переписаны новой настройкой: %d/%d вместо 30/30",
			again.TrialMealLimit, again.TrialScanLimit)
	}

	read, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	if read.TrialMealLimit != 30 || read.TrialScanLimit != 30 {
		t.Errorf("из базы прочитано %d/%d, ждали 30/30", read.TrialMealLimit, read.TrialScanLimit)
	}
}
