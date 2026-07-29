package store

// Тесты кэша разбора против настоящего Postgres: проверяемые свойства (UNIQUE-ключ,
// отсечение по TTL прямо в запросе, счётчик попаданий под UPDATE…RETURNING) держатся
// на семантике БД, а не на коде вокруг неё.

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestAnalysisCache_ТотЖеФайлТотЖеОтвет(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 7001)

	want := []byte(`{"status":"ok","score":38}`)
	if err := s.PutAnalysisCache(ctx, uid, CacheKindScan, "abc123", want); err != nil {
		t.Fatalf("запись в кэш: %v", err)
	}
	got, found, err := s.GetAnalysisCache(ctx, uid, CacheKindScan, "abc123")
	if err != nil {
		t.Fatalf("чтение кэша: %v", err)
	}
	if !found {
		t.Fatal("запись не найдена сразу после записи")
	}
	assertSameJSON(t, got, want)
}

// assertSameJSON сравнивает по СОДЕРЖИМОМУ, а не побайтово: колонка jsonb хранит
// разобранное дерево, поэтому возвращает свой порядок ключей и своё форматирование.
// Для кэша это безразлично — обратно он читается через json.Unmarshal в структуру,
// которой порядок полей не важен; байтовое сравнение здесь проверяло бы Postgres,
// а не нас.
func assertSameJSON(t *testing.T, got, want []byte) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("разбор полученного JSON (%s): %v", got, err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("разбор ожидаемого JSON (%s): %v", want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("результат из кэша = %s, ждали %s", got, want)
	}
}

// Ключ — тройка (пользователь, сканер, хэш). Промах по любому из трёх — это промах,
// а не «похожая запись»: чужой разбор отдавать нельзя, а один файл в сканере составов
// и в счётчике калорий даёт два несовместимых ответа.
func TestAnalysisCache_КлючРазделяетЮзеровИСканеры(t *testing.T) {
	s, ctx := newStore(t)
	alice := mkUser(t, s, ctx, 7002)
	bob := mkUser(t, s, ctx, 7003)

	if err := s.PutAnalysisCache(ctx, alice, CacheKindScan, "sha", []byte(`{"a":1}`)); err != nil {
		t.Fatalf("запись: %v", err)
	}

	cases := []struct {
		name string
		uid  int64
		kind string
		sha  string
	}{
		{"другой пользователь", bob, CacheKindScan, "sha"},
		{"другой сканер", alice, CacheKindMeal, "sha"},
		{"другой хэш", alice, CacheKindScan, "sha2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, found, err := s.GetAnalysisCache(ctx, c.uid, c.kind, c.sha); err != nil || found {
				t.Errorf("found=%v err=%v — ждали промах", found, err)
			}
		})
	}
}

// Повторная запись по тому же ключу перезаписывает результат: сюда попадают только
// реально выполненные прогоны, и свежий ответ вернее старого.
func TestAnalysisCache_ПовторнаяЗаписьПерезаписывает(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 7004)

	_ = s.PutAnalysisCache(ctx, uid, CacheKindScan, "sha", []byte(`{"score":81}`))
	if err := s.PutAnalysisCache(ctx, uid, CacheKindScan, "sha", []byte(`{"score":38}`)); err != nil {
		t.Fatalf("перезапись: %v", err)
	}
	got, _, err := s.GetAnalysisCache(ctx, uid, CacheKindScan, "sha")
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	assertSameJSON(t, got, []byte(`{"score":38}`))

	var n int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM analysis_cache`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("строк в кэше = %d, ждали 1 (UNIQUE-ключ, а не вторая запись)", n)
	}
}

// Протухшая запись НЕ отдаётся, даже если подметальщик до неё ещё не дошёл: срок
// проверяется в самом SELECT. Иначе после сбоя подметания пользователь получал бы
// разбор годовой давности как актуальный.
func TestAnalysisCache_ПротухшаяНеОтдаётся(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 7005)

	_ = s.PutAnalysisCache(ctx, uid, CacheKindScan, "old", []byte(`{"score":50}`))
	agePastTTL(t, ctx, "old", cacheTTL+24*time.Hour)

	if _, found, err := s.GetAnalysisCache(ctx, uid, CacheKindScan, "old"); err != nil || found {
		t.Errorf("found=%v err=%v — протухшая запись не должна отдаваться", found, err)
	}
}

func TestAnalysisCache_ЧисткаУдаляетТолькоПротухшее(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 7006)

	_ = s.PutAnalysisCache(ctx, uid, CacheKindScan, "fresh", []byte(`{"a":1}`))
	_ = s.PutAnalysisCache(ctx, uid, CacheKindScan, "stale", []byte(`{"a":2}`))
	agePastTTL(t, ctx, "stale", cacheTTL+time.Hour)

	n, err := s.PurgeAnalysisCache(ctx)
	if err != nil {
		t.Fatalf("чистка: %v", err)
	}
	if n != 1 {
		t.Errorf("удалено %d записей, ждали 1", n)
	}
	if _, found, _ := s.GetAnalysisCache(ctx, uid, CacheKindScan, "fresh"); !found {
		t.Error("свежая запись пропала после чистки")
	}
}

// Попадания считаются: по hit_count видно, востребован ли кэш вообще, без отдельной
// телеметрии.
func TestAnalysisCache_СчётчикПопаданий(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 7007)

	_ = s.PutAnalysisCache(ctx, uid, CacheKindMeal, "sha", []byte(`{"a":1}`))
	for i := 0; i < 3; i++ {
		if _, found, _ := s.GetAnalysisCache(ctx, uid, CacheKindMeal, "sha"); !found {
			t.Fatalf("попадание %d не сработало", i+1)
		}
	}

	var hits int
	if err := testPool.QueryRow(ctx,
		`SELECT hit_count FROM analysis_cache WHERE image_sha = 'sha'`).Scan(&hits); err != nil {
		t.Fatalf("чтение счётчика: %v", err)
	}
	if hits != 3 {
		t.Errorf("hit_count = %d, ждали 3", hits)
	}
}

// agePastTTL состаривает запись, сдвигая created_at назад: ждать 90 дней в тесте
// нечем, а подменять время в БД — честнее, чем прятать TTL за инъекцию часов.
func agePastTTL(t *testing.T, ctx context.Context, sha string, age time.Duration) {
	t.Helper()
	_, err := testPool.Exec(ctx,
		`UPDATE analysis_cache SET created_at = now() - $2::interval WHERE image_sha = $1`,
		sha, age.String())
	if err != nil {
		t.Fatalf("состаривание записи: %v", err)
	}
}
