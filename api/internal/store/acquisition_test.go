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
	if _, err := s.UpsertUser(ctx, tgID, "vasya", "tg_ovz"); err != nil {
		t.Fatalf("первый UpsertUser: %v", err)
	}
	if got := readSource(t, ctx, tgID); got != "tg_ovz" {
		t.Fatalf("после первого касания source=%q, ждали tg_ovz", got)
	}

	// Повторный заход БЕЗ источника (обычный /start, вход в mini-app) — не затирает.
	if _, err := s.UpsertUser(ctx, tgID, "vasya", ""); err != nil {
		t.Fatalf("повторный UpsertUser: %v", err)
	}
	if got := readSource(t, ctx, tgID); got != "tg_ovz" {
		t.Errorf("пустой источник затёр канал: source=%q, ждали tg_ovz", got)
	}

	// Приход по ДРУГОМУ диплинку — тоже не перебивает: первый канал и привёл человека.
	if _, err := s.UpsertUser(ctx, tgID, "vasya", "tg_other"); err != nil {
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

	if _, err := s.UpsertUser(ctx, tgID, "petya", ""); err != nil {
		t.Fatalf("первый UpsertUser: %v", err)
	}
	if got := readSource(t, ctx, tgID); got != "" {
		t.Fatalf("источник взялся из ниоткуда: %q", got)
	}
	if _, err := s.UpsertUser(ctx, tgID, "petya", "tg_ovz"); err != nil {
		t.Fatalf("второй UpsertUser: %v", err)
	}
	if got := readSource(t, ctx, tgID); got != "tg_ovz" {
		t.Errorf("канал не дописался к пустому: source=%q, ждали tg_ovz", got)
	}
}
