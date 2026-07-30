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
	if err := s.PutAnalysisCache(ctx, uid, CacheKindScan, "abc123", want, CacheStrengthBase); err != nil {
		t.Fatalf("запись в кэш: %v", err)
	}
	got, _, found, err := s.GetAnalysisCache(ctx, uid, CacheKindScan, "abc123")
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

	if err := s.PutAnalysisCache(ctx, alice, CacheKindScan, "sha", []byte(`{"a":1}`), CacheStrengthBase); err != nil {
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
			if _, _, found, err := s.GetAnalysisCache(ctx, c.uid, c.kind, c.sha); err != nil || found {
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

	_ = s.PutAnalysisCache(ctx, uid, CacheKindScan, "sha", []byte(`{"score":81}`), CacheStrengthBase)
	if err := s.PutAnalysisCache(ctx, uid, CacheKindScan, "sha", []byte(`{"score":38}`), CacheStrengthBase); err != nil {
		t.Fatalf("перезапись: %v", err)
	}
	got, _, _, err := s.GetAnalysisCache(ctx, uid, CacheKindScan, "sha")
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

	_ = s.PutAnalysisCache(ctx, uid, CacheKindScan, "old", []byte(`{"score":50}`), CacheStrengthBase)
	agePastTTL(t, ctx, "old", cacheTTL+24*time.Hour)

	if _, _, found, err := s.GetAnalysisCache(ctx, uid, CacheKindScan, "old"); err != nil || found {
		t.Errorf("found=%v err=%v — протухшая запись не должна отдаваться", found, err)
	}
}

func TestAnalysisCache_ЧисткаУдаляетТолькоПротухшее(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 7006)

	_ = s.PutAnalysisCache(ctx, uid, CacheKindScan, "fresh", []byte(`{"a":1}`), CacheStrengthBase)
	_ = s.PutAnalysisCache(ctx, uid, CacheKindScan, "stale", []byte(`{"a":2}`), CacheStrengthBase)
	agePastTTL(t, ctx, "stale", cacheTTL+time.Hour)

	n, err := s.PurgeAnalysisCache(ctx)
	if err != nil {
		t.Fatalf("чистка: %v", err)
	}
	if n != 1 {
		t.Errorf("удалено %d записей, ждали 1", n)
	}
	if _, _, found, _ := s.GetAnalysisCache(ctx, uid, CacheKindScan, "fresh"); !found {
		t.Error("свежая запись пропала после чистки")
	}
}

// Попадания считаются: по hit_count видно, востребован ли кэш вообще, без отдельной
// телеметрии.
func TestAnalysisCache_СчётчикПопаданий(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 7007)

	_ = s.PutAnalysisCache(ctx, uid, CacheKindMeal, "sha", []byte(`{"a":1}`), CacheStrengthBase)
	for i := 0; i < 3; i++ {
		if _, _, found, _ := s.GetAnalysisCache(ctx, uid, CacheKindMeal, "sha"); !found {
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

// Главное свойство правила приоритета: разбор, прошедший эскалацию на Sonnet,
// вытесняет закэшированный ответ основной модели. Это ровно кейс онигири — первым
// пришёл выдуманный Haiku-ответ (81), верным был эскалированный (38).
func TestAnalysisCache_СильныйВытесняетСлабый(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 7008)

	_ = s.PutAnalysisCache(ctx, uid, CacheKindScan, "sha", []byte(`{"score":81}`), CacheStrengthBase)
	if err := s.PutAnalysisCache(ctx, uid, CacheKindScan, "sha", []byte(`{"score":38}`), CacheStrengthStrong); err != nil {
		t.Fatalf("запись сильного ответа: %v", err)
	}

	got, _, _, err := s.GetAnalysisCache(ctx, uid, CacheKindScan, "sha")
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	assertSameJSON(t, got, []byte(`{"score":38}`))
	assertStrength(t, ctx, "sha", CacheStrengthStrong)
}

// Обратное направление: слабый ответ поверх сильного НЕ пишется. Иначе гонка двух
// параллельных отправок одного файла решалась бы порядком записи, и Haiku мог бы
// затереть уже полученный ответ Sonnet.
func TestAnalysisCache_СлабыйНеЗатираетСильный(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 7009)

	_ = s.PutAnalysisCache(ctx, uid, CacheKindScan, "sha", []byte(`{"score":38}`), CacheStrengthStrong)
	if err := s.PutAnalysisCache(ctx, uid, CacheKindScan, "sha", []byte(`{"score":81}`), CacheStrengthBase); err != nil {
		t.Fatalf("запись слабого ответа не должна быть ошибкой: %v", err)
	}

	got, _, _, err := s.GetAnalysisCache(ctx, uid, CacheKindScan, "sha")
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	assertSameJSON(t, got, []byte(`{"score":38}`))
	assertStrength(t, ctx, "sha", CacheStrengthStrong)
}

// При РАВНОЙ силе побеждает свежий: это случай прогона после истечения TTL, где
// старый ответ уже не отдаётся и держать его незачем.
func TestAnalysisCache_ПриРавнойСилеПобеждаетСвежий(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 7010)

	_ = s.PutAnalysisCache(ctx, uid, CacheKindScan, "sha", []byte(`{"score":10}`), CacheStrengthStrong)
	_ = s.PutAnalysisCache(ctx, uid, CacheKindScan, "sha", []byte(`{"score":20}`), CacheStrengthStrong)

	got, _, _, err := s.GetAnalysisCache(ctx, uid, CacheKindScan, "sha")
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	assertSameJSON(t, got, []byte(`{"score":20}`))
}

// Отклонённая запись не должна побочно сбрасывать счётчик попаданий: hit_count —
// метрика востребованности кэша, и её нельзя обнулять неудавшейся перезаписью.
func TestAnalysisCache_ОтклонённаяЗаписьНеСбрасываетСчётчик(t *testing.T) {
	s, ctx := newStore(t)
	uid := mkUser(t, s, ctx, 7011)

	_ = s.PutAnalysisCache(ctx, uid, CacheKindScan, "sha", []byte(`{"score":38}`), CacheStrengthStrong)
	_, _, _, _ = s.GetAnalysisCache(ctx, uid, CacheKindScan, "sha")
	_, _, _, _ = s.GetAnalysisCache(ctx, uid, CacheKindScan, "sha")

	_ = s.PutAnalysisCache(ctx, uid, CacheKindScan, "sha", []byte(`{"score":81}`), CacheStrengthBase)

	var hits int
	if err := testPool.QueryRow(ctx,
		`SELECT hit_count FROM analysis_cache WHERE image_sha = 'sha'`).Scan(&hits); err != nil {
		t.Fatalf("чтение счётчика: %v", err)
	}
	if hits != 2 {
		t.Errorf("hit_count = %d, ждали 2 (отклонённая запись счётчик не трогает)", hits)
	}
}

func assertStrength(t *testing.T, ctx context.Context, sha string, want int16) {
	t.Helper()
	var got int16
	if err := testPool.QueryRow(ctx,
		`SELECT strength FROM analysis_cache WHERE image_sha = $1`, sha).Scan(&got); err != nil {
		t.Fatalf("чтение strength: %v", err)
	}
	if got != want {
		t.Errorf("strength = %d, ждали %d", got, want)
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
