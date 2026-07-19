package server

// Диагностика пула соединений для нагрузочного стенда (DEBUG_STATS=1).
//
// pgxpool.Stat() отдаёт МГНОВЕННЫЙ снимок: сколько коннектов занято прямо сейчас.
// Для нагрузочного отчёта нужен пик за прогон — «сколько коннектов реально
// понадобилось», — поэтому фоновый семплер опрашивает пул и запоминает максимум.
// Работает только при DEBUG_STATS=1: на проде лишней горутины нет.

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// poolPeakSampleInterval — шаг опроса. 100мс хватает, чтобы поймать пик на прогоне в
// минуты, и не создаёт заметной нагрузки (Stat() читает счётчики без блокировок).
const poolPeakSampleInterval = 100 * time.Millisecond

// poolPeak хранит максимум занятых коннектов, замеченный семплером.
type poolPeak struct{ v atomic.Int32 }

func (p *poolPeak) observe(n int32) {
	for {
		cur := p.v.Load()
		if n <= cur || p.v.CompareAndSwap(cur, n) {
			return
		}
	}
}

func (p *poolPeak) get() int32 { return p.v.Load() }

// StartPoolSampler запускает семплер пика занятых коннектов. Вызывается только при
// DEBUG_STATS=1; завершается вместе с ctx.
func (s *Server) StartPoolSampler(ctx context.Context) {
	if !s.cfg.DebugStats || s.pool == nil {
		return
	}
	t := time.NewTicker(poolPeakSampleInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.poolPeak.observe(s.pool.Stat().AcquiredConns())
		}
	}
}

// poolStatsView — статистика пула для отчёта нагрузочного теста.
//
// Как читать. acquired_max — сколько коннектов реально понадобилось на пике; по нему
// и подбирается MaxConns. empty_acquire_count считает захваты, которым не досталось
// СВОБОДНОГО коннекта, — но сюда попадает и прогрев (первые запросы, пока пул пуст и
// коннекты только создаются), поэтому единицы на старте — норма, а не дефицит.
// Настоящее насыщение выглядит иначе: acquired_max упёрся в max_conns И
// empty_acquire_count растёт вместе с нагрузкой.
type poolStatsView struct {
	MaxConns             int32  `json:"max_conns"`
	TotalConns           int32  `json:"total_conns"`
	AcquiredConns        int32  `json:"acquired_conns"`
	AcquiredMax          int32  `json:"acquired_max"`
	IdleConns            int32  `json:"idle_conns"`
	AcquireCount         int64  `json:"acquire_count"`
	EmptyAcquireCount    int64  `json:"empty_acquire_count"`
	CanceledAcquireCount int64  `json:"canceled_acquire_count"`
	AcquireDurationMs    int64  `json:"acquire_duration_total_ms"`
	NewConnsCount        int64  `json:"new_conns_count"`
	Note                 string `json:"note,omitempty"`
}

func poolStats(st *pgxpool.Stat, peak int32) poolStatsView {
	v := poolStatsView{
		MaxConns: st.MaxConns(), TotalConns: st.TotalConns(),
		AcquiredConns: st.AcquiredConns(), AcquiredMax: peak,
		IdleConns: st.IdleConns(), AcquireCount: st.AcquireCount(),
		EmptyAcquireCount: st.EmptyAcquireCount(), CanceledAcquireCount: st.CanceledAcquireCount(),
		AcquireDurationMs: st.AcquireDuration().Milliseconds(), NewConnsCount: st.NewConnsCount(),
	}
	// Признак насыщения — именно пик, упёршийся в потолок: сам по себе
	// empty_acquire_count включает прогрев и о дефиците не говорит.
	if peak >= st.MaxConns() {
		v.Note = "пик занятых упёрся в max_conns — пул был узким местом"
	}
	return v
}
