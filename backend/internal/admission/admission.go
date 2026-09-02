// Package admission bounds non-control work so calling state and live commands
// retain capacity within the portal's existing connection budget.
package admission

import (
	"context"
	"errors"
	"sync"
)

type Class uint8

const (
	Background Class = iota
	CallingSync
	CallingControl
)

var ErrFull = errors.New("non-control capacity is full")

type classKey struct{}

// WithClass is set by the HTTP adapter from its registered route, never from
// request headers or other client-provided priority hints.
func WithClass(ctx context.Context, class Class) context.Context {
	return context.WithValue(ctx, classKey{}, class)
}

func ClassOf(ctx context.Context) Class {
	class, _ := ctx.Value(classKey{}).(Class)
	if class != CallingSync && class != CallingControl {
		return Background
	}
	return class
}

type Gate struct {
	background chan struct{}
	nonControl chan struct{}
}

// New reserves one slot for Calling sync and another for commands when at
// least three connections exist. Small test adapters remain usable, but a
// single-connection pool cannot reserve headroom. The production portal
// executor requires at least three connections.
func New(poolMaximum int32) *Gate {
	return &Gate{
		background: make(chan struct{}, max(int(poolMaximum)-2, 1)),
		nonControl: make(chan struct{}, max(int(poolMaximum)-1, 1)),
	}
}

// Acquire fails immediately when non-control capacity is full. The caller owns
// the permit until its complete handler, connection, rows, or transaction ends.
func (gate *Gate) Acquire(ctx context.Context) (func(), error) {
	return gate.acquire(ctx, false)
}

// Wait shares Acquire's capacity policy but waits for a permit until the
// caller's context ends. HTTP callers supply a short admission-only deadline;
// database acquisition continues to use the immediate Acquire path.
func (gate *Gate) Wait(ctx context.Context) (func(), error) {
	return gate.acquire(ctx, true)
}

func (gate *Gate) acquire(ctx context.Context, wait bool) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	class := ClassOf(ctx)
	if class == CallingControl {
		return func() {}, nil
	}
	if class == Background {
		if err := acquireSlot(ctx, gate.background, wait); err != nil {
			return nil, err
		}
	}
	if err := acquireSlot(ctx, gate.nonControl, wait); err != nil {
		if class == Background {
			<-gate.background
		}
		return nil, err
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			<-gate.nonControl
			if class == Background {
				<-gate.background
			}
		})
	}
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func acquireSlot(ctx context.Context, slot chan struct{}, wait bool) error {
	if !wait {
		select {
		case slot <- struct{}{}:
			return nil
		default:
			return ErrFull
		}
	}
	select {
	case slot <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
