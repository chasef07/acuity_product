package httpapi

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/api"
)

const (
	signUpEligibilityWindow = time.Minute
	signUpEligibilityLimit  = 20
)

type signUpEligibilityLimiter struct {
	mu          sync.Mutex
	windowStart time.Time
	counts      map[string]int
}

func (limiter *signUpEligibilityLimiter) allow(client string, now time.Time) (bool, time.Duration) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if now.Sub(limiter.windowStart) >= signUpEligibilityWindow {
		limiter.windowStart = now
		limiter.counts = map[string]int{}
	}
	if limiter.counts[client] >= signUpEligibilityLimit {
		return false, limiter.windowStart.Add(signUpEligibilityWindow).Sub(now)
	}
	limiter.counts[client]++
	return true, 0
}

func signUpEligibilityClient(r *http.Request) string {
	if forwarded := r.Header.Values("X-Forwarded-For"); len(forwarded) > 0 {
		hops := strings.Split(forwarded[len(forwarded)-1], ",")
		if client := strings.TrimSpace(hops[len(hops)-1]); client != "" {
			return client
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (server *Server) InspectSignUpEligibility(w http.ResponseWriter, r *http.Request) {
	if !server.portalOnly(w, r) {
		return
	}
	if allowed, retryAfter := server.signUpLimiter.allow(signUpEligibilityClient(r), time.Now()); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAfter.Round(time.Second).Seconds()))))
		server.writeError(w, r, http.StatusTooManyRequests, "RATE_LIMITED", "Too many sign-up checks. Try again shortly.", true)
		return
	}
	var body api.SignUpEligibilityRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	if err := server.access.InspectSignUpEligibility(ctx, string(body.Email)); err != nil {
		server.writeAccessError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
