package store

import (
	"context"
	"sync"
)

// Lazy serves requests before the database connection exists.
// Render starts health checks as soon as the port is open, and a free
// Postgres instance can still be provisioning at that moment.
type Lazy struct {
	mu    sync.RWMutex
	inner Store
}

func NewLazy() *Lazy { return &Lazy{} }

func (l *Lazy) Ready(inner Store) {
	l.mu.Lock()
	l.inner = inner
	l.mu.Unlock()
}

func (l *Lazy) current() (Store, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.inner == nil {
		return nil, ErrUnavailable
	}
	return l.inner, nil
}

func (l *Lazy) Put(ctx context.Context, code string, rec Record) error {
	inner, err := l.current()
	if err != nil {
		return err
	}
	return inner.Put(ctx, code, rec)
}

func (l *Lazy) Take(ctx context.Context, code string) (string, error) {
	inner, err := l.current()
	if err != nil {
		return "", err
	}
	return inner.Take(ctx, code)
}

func (l *Lazy) Sweep(ctx context.Context) error {
	inner, err := l.current()
	if err != nil {
		return err
	}
	return inner.Sweep(ctx)
}
