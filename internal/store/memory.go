package store

import (
	"context"
	"sync"
	"time"
)

type Memory struct {
	mu   sync.Mutex
	rows map[string]Record
}

func NewMemory() *Memory {
	return &Memory{rows: map[string]Record{}}
}

func (m *Memory) Put(_ context.Context, code string, rec Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rows[code]; ok {
		return ErrExists
	}
	m.rows[code] = rec
	return nil
}

func (m *Memory) Take(_ context.Context, code string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.rows[code]
	if !ok {
		return "", ErrNotFound
	}
	if rec.ExpiresAt != nil && !rec.ExpiresAt.After(time.Now()) {
		delete(m.rows, code)
		return "", ErrNotFound
	}
	if rec.Once {
		delete(m.rows, code)
	}
	return rec.Payload, nil
}

func (m *Memory) Sweep(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for code, rec := range m.rows {
		if rec.ExpiresAt != nil && !rec.ExpiresAt.After(now) {
			delete(m.rows, code)
		}
	}
	return nil
}
