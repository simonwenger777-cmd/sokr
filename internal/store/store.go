package store

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("exists")
)

type Record struct {
	Payload   string
	Once      bool
	ExpiresAt *time.Time
}

type Store interface {
	Put(ctx context.Context, code string, rec Record) error
	Take(ctx context.Context, code string) (string, error)
	Sweep(ctx context.Context) error
}
