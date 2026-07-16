package server

import (
	"sync"
	"time"
)

// supportWindow — сколько ждём сообщение после нажатия «Написать в поддержку» (или
// голого /support), прежде чем режим сам погаснет. Достаточно, чтобы человек успел
// сформулировать, но не настолько, чтобы случайное сообщение через час улетело в
// поддержку как обращение.
const supportWindow = 15 * time.Minute

// supportState помнит пользователей, вошедших в режим поддержки: их следующее
// обычное сообщение уходит в поддержку без префикса /support. Живёт в памяти
// процесса (монолит, §16) — при рестарте забывается, пользователь просто нажмёт
// кнопку заново. Терять здесь нечего: источник истины по обращениям — БД, а режим
// лишь снимает трение ввода.
type supportState struct {
	mu      sync.Mutex
	pending map[int64]time.Time // user_id → дедлайн окна
}

func newSupportState() *supportState {
	return &supportState{pending: make(map[int64]time.Time)}
}

// arm включает режим поддержки для пользователя на supportWindow.
func (s *supportState) arm(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[userID] = time.Now().Add(supportWindow)
}

// consume одноразово гасит режим и сообщает, был ли он активен (и не просрочен).
// true → это сообщение считаем обращением в поддержку.
func (s *supportState) consume(userID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	deadline, ok := s.pending[userID]
	if !ok {
		return false
	}
	delete(s.pending, userID)
	return time.Now().Before(deadline)
}
