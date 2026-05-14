package bot

import "sync"

// userState tracks what a user is currently doing (which button they pressed).
type userState int

const (
	stateIdle   userState = iota
	stateGame             // waiting for game proposal text
	stateStream           // waiting for stream proposal text
	stateAnon             // waiting for anonymous proposal text
)

// stateStore is a concurrency-safe in-memory FSM store.
// For a serverless environment (stateless between requests) this works because
// Telegram sends messages sequentially per user and Vercel handles one webhook
// at a time per deployment. For higher concurrency, replace with Redis.
type stateStore struct {
	mu    sync.Mutex
	states map[int64]userState
}

var globalState = &stateStore{states: make(map[int64]userState)}

func (s *stateStore) get(userID int64) userState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.states[userID]
}

func (s *stateStore) set(userID int64, st userState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[userID] = st
}

func (s *stateStore) clear(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, userID)
}
