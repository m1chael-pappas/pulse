package claudecode

import (
	"sync"
	"time"
)

// rateLimitGate tracks an active backoff window for the OAuth usage endpoint.
// CodexBar uses the same pattern — once Anthropic returns 429 we stop
// hammering until the window expires, which keeps us off their naughty list
// and prevents the tile from flashing "rate-limited" every refresh tick.
type rateLimitGate struct {
	mu      sync.Mutex
	blocked time.Time
}

var oauthRateGate rateLimitGate

const defaultRateLimitBackoff = 5 * time.Minute

func (g *rateLimitGate) blockedUntil() (time.Time, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.blocked.IsZero() || time.Now().After(g.blocked) {
		return time.Time{}, false
	}
	return g.blocked, true
}

func (g *rateLimitGate) trip(retryAfter time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if retryAfter <= 0 {
		retryAfter = defaultRateLimitBackoff
	}
	g.blocked = time.Now().Add(retryAfter)
}

func (g *rateLimitGate) clear() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.blocked = time.Time{}
}
