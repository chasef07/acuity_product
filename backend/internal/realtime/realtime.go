package realtime

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

const notificationChannel = "acuity_workspace_hints"

type Config struct {
	DatabaseURL        string
	AccessTimeout      time.Duration
	HeartbeatInterval  time.Duration
	StreamLifetime     time.Duration
	RevalidateInterval time.Duration
	ReconnectMin       time.Duration
	ReconnectMax       time.Duration
}

type Hint struct {
	PracticeID string `json:"practiceId"`
	Version    int64  `json:"version"`
}

// Hub turns PostgreSQL notifications into disposable, authorized SSE hints.
// PostgreSQL rows remain authoritative and reconnect always causes a refetch.
type Hub struct {
	config Config
	access *access.Module

	mu          sync.RWMutex
	subscribers map[string]map[chan Hint]struct{}
	ready       atomic.Bool
}

func New(config Config, accessModule *access.Module) (*Hub, error) {
	if config.DatabaseURL == "" || accessModule == nil {
		return nil, fmt.Errorf("database URL and Access module are required")
	}
	if config.AccessTimeout <= 0 ||
		config.HeartbeatInterval <= 0 ||
		config.StreamLifetime <= config.HeartbeatInterval ||
		config.RevalidateInterval <= 0 ||
		config.ReconnectMin <= 0 ||
		config.ReconnectMax < config.ReconnectMin {
		return nil, fmt.Errorf("positive bounded realtime intervals are required")
	}
	return &Hub{
		config:      config,
		access:      accessModule,
		subscribers: map[string]map[chan Hint]struct{}{},
	}, nil
}

func (hub *Hub) Ready() bool {
	return hub.ready.Load()
}

// Run owns one dedicated direct LISTEN connection and reconnects with bounded
// exponential backoff and jitter. Notifications are never treated as state.
func (hub *Hub) Run(ctx context.Context) {
	backoff := hub.config.ReconnectMin
	for ctx.Err() == nil {
		connection, err := pgx.Connect(ctx, hub.config.DatabaseURL)
		if err != nil {
			hub.ready.Store(false)
			if !wait(ctx, jitter(backoff)) {
				return
			}
			backoff = min(backoff*2, hub.config.ReconnectMax)
			continue
		}
		if _, err := connection.Exec(ctx, "LISTEN "+notificationChannel); err != nil {
			hub.ready.Store(false)
			_ = connection.Close(ctx)
			if !wait(ctx, jitter(backoff)) {
				return
			}
			backoff = min(backoff*2, hub.config.ReconnectMax)
			continue
		}

		hub.ready.Store(true)
		backoff = hub.config.ReconnectMin
		for ctx.Err() == nil {
			notification, err := connection.WaitForNotification(ctx)
			if err != nil {
				break
			}
			var hint Hint
			if err := json.Unmarshal([]byte(notification.Payload), &hint); err != nil ||
				hint.PracticeID == "" ||
				hint.Version < 1 {
				continue
			}
			hub.publish(hint)
		}
		hub.ready.Store(false)
		_ = connection.Close(context.Background())
		if ctx.Err() == nil && !wait(ctx, jitter(backoff)) {
			return
		}
		backoff = min(backoff*2, hub.config.ReconnectMax)
	}
}

func (hub *Hub) Stream(
	w http.ResponseWriter,
	r *http.Request,
	identity access.Identity,
	practiceID string,
	locationID string,
) error {
	accessContext, cancelAccess := context.WithTimeout(r.Context(), hub.config.AccessTimeout)
	authorization, err := hub.access.ResolveActor(accessContext, identity, practiceID, locationID)
	cancelAccess()
	if err != nil {
		return err
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		return errors.New("streaming response is unavailable")
	}
	hints, unsubscribe := hub.subscribe(practiceID)
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if err := writeEvent(w, "ready", Hint{
		PracticeID: practiceID,
		Version:    authorization.Practice.Version,
	}); err != nil {
		return nil
	}
	flusher.Flush()

	heartbeat := time.NewTicker(hub.config.HeartbeatInterval)
	defer heartbeat.Stop()
	revalidate := time.NewTicker(hub.config.RevalidateInterval)
	defer revalidate.Stop()
	lifetime := time.NewTimer(hub.config.StreamLifetime)
	defer lifetime.Stop()

	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-lifetime.C:
			return nil
		case hint := <-hints:
			if err := writeEvent(w, "hint", hint); err != nil {
				return nil
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return nil
			}
			flusher.Flush()
		case <-revalidate.C:
			accessContext, cancelAccess := context.WithTimeout(r.Context(), hub.config.AccessTimeout)
			_, err := hub.access.ResolveActor(accessContext, identity, practiceID, locationID)
			cancelAccess()
			if err != nil {
				return nil
			}
		}
	}
}

func (hub *Hub) subscribe(practiceID string) (<-chan Hint, func()) {
	channel := make(chan Hint, 1)
	hub.mu.Lock()
	if hub.subscribers[practiceID] == nil {
		hub.subscribers[practiceID] = map[chan Hint]struct{}{}
	}
	hub.subscribers[practiceID][channel] = struct{}{}
	hub.mu.Unlock()

	return channel, func() {
		hub.mu.Lock()
		delete(hub.subscribers[practiceID], channel)
		if len(hub.subscribers[practiceID]) == 0 {
			delete(hub.subscribers, practiceID)
		}
		hub.mu.Unlock()
	}
}

func (hub *Hub) publish(hint Hint) {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	for subscriber := range hub.subscribers[hint.PracticeID] {
		latest := hint
		select {
		case subscriber <- latest:
		default:
			select {
			case queued := <-subscriber:
				if queued.Version > latest.Version {
					latest = queued
				}
			default:
			}
			select {
			case subscriber <- latest:
			default:
			}
		}
	}
}

func writeEvent(w http.ResponseWriter, event string, hint Hint) error {
	payload, err := json.Marshal(hint)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
	return err
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func jitter(base time.Duration) time.Duration {
	if base <= time.Millisecond {
		return base
	}
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return base
	}
	spread := base / 2
	offset := time.Duration(binary.LittleEndian.Uint64(value[:]) % uint64(spread))
	return base - spread/2 + offset
}
