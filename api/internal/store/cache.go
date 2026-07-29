package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Кэш разбора по хэшу изображения (миграция 0016). Задача — убрать ситуацию, в
// которой один и тот же файл даёт разные вердикты: повторная отправка того же фото
// возвращает СОХРАНЁННЫЙ результат, мгновенно и без списания квоты.
//
// Что здесь НЕ делается сознательно:
//   - не храним само фото (сквозной принцип приватности: ни scans, ни meals его не
//     хранят) — только хэш и разобранный результат;
//   - не рассчитываем на побайтовый round-trip: колонка result — jsonb, она хранит
//     РАЗОБРАННОЕ дерево и возвращает свой порядок ключей и своё форматирование.
//     Читается кэш через json.Unmarshal в структуру, которой это безразлично; jsonb
//     выбран ради аналитики по кэшу (какой балл на каком фото замёрз);
//   - не инвалидируем кэш по смене модели/промпта. Смена модели меняет ответ на
//     то же фото, и это ровно то, от чего кэш защищает пользователя: он один раз
//     увидел вердикт, и вердикт не должен «поплыть» после нашего деплоя. Нужен
//     сброс — чистится таблица целиком (см. RELEASE.md).
const (
	// CacheKindScan — сканер составов (POST /api/scan).
	CacheKindScan = "scan"
	// CacheKindMeal — счётчик калорий по фото (POST /api/meals).
	CacheKindMeal = "meal"

	// cacheTTL — срок жизни записи. 90 дней, а не «навсегда»: за этим горизонтом
	// рецептура продукта на полке успевает смениться, и отдавать старый разбор как
	// текущий уже нечестно. Проверяется на чтении (см. GetAnalysisCache), так что
	// протухшая запись не отдаётся, даже если подметание отстало.
	cacheTTL = 90 * 24 * time.Hour
)

// GetAnalysisCache возвращает сохранённый результат разбора для (userID, kind, sha).
// found=false — записи нет или она старше TTL. Попадание отмечается счётчиком
// hit_count/last_hit_at: по нему видно, насколько кэш вообще востребован, без
// отдельной телеметрии.
func (s *Store) GetAnalysisCache(ctx context.Context, userID int64, kind, sha string) (result []byte, found bool, err error) {
	const q = `
UPDATE analysis_cache
   SET hit_count = hit_count + 1, last_hit_at = now()
 WHERE user_id = $1 AND kind = $2 AND image_sha = $3
   AND created_at > now() - $4::interval
RETURNING result`
	err = s.pool.QueryRow(ctx, q, userID, kind, sha, cacheTTL.String()).Scan(&result)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return result, true, nil
}

// PutAnalysisCache сохраняет результат разбора. Повторная запись по тому же ключу
// ПЕРЕЗАПИСЫВАЕТ результат и сбрасывает created_at: сюда попадают только реально
// выполненные прогоны (на попадании в кэш пайплайн не запускается), то есть это
// либо первый разбор, либо разбор после истечения TTL — в обоих случаях свежий
// ответ вернее старого.
func (s *Store) PutAnalysisCache(ctx context.Context, userID int64, kind, sha string, result []byte) error {
	const q = `
INSERT INTO analysis_cache (user_id, kind, image_sha, result)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, kind, image_sha)
DO UPDATE SET result = EXCLUDED.result, created_at = now(), hit_count = 0, last_hit_at = NULL`
	_, err := s.pool.Exec(ctx, q, userID, kind, sha, result)
	return err
}

// PurgeAnalysisCache удаляет записи старше TTL и возвращает их число. Вызывается
// подметальщиком (cmd/subsweep) вместе с прочей периодикой — отдельного демона под
// это заводить незачем.
func (s *Store) PurgeAnalysisCache(ctx context.Context) (int64, error) {
	const q = `DELETE FROM analysis_cache WHERE created_at <= now() - $1::interval`
	tag, err := s.pool.Exec(ctx, q, cacheTTL.String())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
