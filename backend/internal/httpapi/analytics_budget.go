package httpapi

import (
	"context"
	"net/http"
	"time"
)

func (server *Server) beginAnalytics(w http.ResponseWriter, r *http.Request) (context.Context, func(), bool) {
	if !server.analyticsActive.CompareAndSwap(false, true) {
		w.Header().Set("Retry-After", "1")
		server.writeError(w, r, http.StatusTooManyRequests, "UNAVAILABLE", "Analytics is busy. Try again in a moment.", true)
		return nil, nil, false
	}
	parent, cancelParent := server.requestContext(r)
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	return ctx, func() { cancel(); cancelParent(); server.analyticsActive.Store(false) }, true
}
