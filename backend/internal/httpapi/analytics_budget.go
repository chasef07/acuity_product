package httpapi

import (
	"context"
	"net/http"
	"sync"
	"time"
)

const (
	analyticsConcurrency = 2
	analyticsTimeout     = 2 * time.Second
)

func (server *Server) beginAnalytics(w http.ResponseWriter, r *http.Request) (context.Context, func(), bool) {
	if !server.acquireAnalytics() {
		w.Header().Set("Retry-After", "1")
		server.writeError(w, r, http.StatusTooManyRequests, "UNAVAILABLE", "Analytics is busy. Try again in a moment.", true)
		return nil, nil, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), min(analyticsTimeout, server.config.RequestTimeout))
	return ctx, sync.OnceFunc(func() { cancel(); server.analyticsActive.Add(-1) }), true
}

func (server *Server) acquireAnalytics() bool {
	for {
		active := server.analyticsActive.Load()
		if active >= analyticsConcurrency {
			return false
		}
		if server.analyticsActive.CompareAndSwap(active, active+1) {
			return true
		}
	}
}
