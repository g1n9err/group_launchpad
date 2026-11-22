package transport

// Login brute-force throttling — a fixed-window, in-memory limiter with
// three bounded key spaces:
//
//	pair    (client IP + normalized email) — the strictest budget, stops
//	         focused guessing against one account from one address;
//	account (normalized email across ALL IPs) — generous cap that slows
//	         password spraying that rotates addresses;
//	ip      (client IP across ALL accounts) — cap that slows one address
//	         cycling through many accounts (credential stuffing).
//
// A blocked request is rejected with 429 RATE_LIMITED BEFORE any bcrypt
// work, identically for existing and non-existing accounts (the limiter is
// not an account-enumeration oracle). A successful login clears the pair
// and account windows for that account, so a legitimate user is never
// locked out by a single attacker address — only sustained failures above
// the (deliberately generous) account cap block briefly, and only for the
// bounded window. The IP window survives success: one good login from a
// shared address must not erase a stuffing count.
//
// Memory is hard-bounded: each map holds at most maxEntries records. Inserts
// prune expired records first, then evict the soonest-to-expire records, so
// an attacker rotating IPs/emails cannot grow the process footprint.
//
// SCOPE: these limits apply PER PROCESS. This is not a distributed rate
// limiter; a multi-instance deployment must move the counters to the shared
// store (the key spaces here map onto it directly). No external service is
// introduced because none is used elsewhere in the repository.

import (
	"sync"
	"time"
)

// loginThrottleMaxEntries bounds each of the three key-space maps.
// 3 × 50,000 records (~a few MB) is far above any legitimate single-instance
// sign-in volume and small enough to be irrelevant to process memory.
const loginThrottleMaxEntries = 50_000

// loginWindow is one key's failure window.
type loginWindow struct {
	fails   int
	resetAt time.Time
}

// loginThrottle is a fixed-window limiter safe for concurrent use.
type loginThrottle struct {
	mu         sync.Mutex
	pairMax    int
	accountMax int
	ipMax      int
	window     time.Duration
	maxEntries int
	pairs      map[string]*loginWindow
	accounts   map[string]*loginWindow
	ips        map[string]*loginWindow
	now        func() time.Time // injectable clock; used for EVERY time read
}

// newLoginThrottle builds the limiter. pairMax is the per IP+email budget;
// the account cap (3×pairMax) is deliberately looser so address rotation is
// slowed without turning the limiter into a cheap account-lockout DoS, and
// the IP cap (10×pairMax) tolerates small NATs while stopping stuffing.
func newLoginThrottle(pairMax int, window time.Duration) *loginThrottle {
	if pairMax <= 0 {
		pairMax = 10
	}
	if window <= 0 {
		window = 5 * time.Minute
	}
	return &loginThrottle{
		pairMax:    pairMax,
		accountMax: 3 * pairMax,
		ipMax:      10 * pairMax,
		window:     window,
		maxEntries: loginThrottleMaxEntries,
		pairs:      map[string]*loginWindow{},
		accounts:   map[string]*loginWindow{},
		ips:        map[string]*loginWindow{},
		now:        time.Now,
	}
}

// blocked reports whether any of the key spaces covering (ip, email) is over
// its budget and, if so, the LONGEST remaining block duration (used for
// Retry-After). Expired records are dropped opportunistically.
func (t *loginThrottle) blocked(ip, email string) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.blockedLocked(ip, email)
}

func (t *loginThrottle) blockedLocked(ip, email string) (bool, time.Duration) {
	now := t.now()
	spaces := []struct {
		key   string
		max   int
		space map[string]*loginWindow
	}{
		{"p|" + ip + "|" + email, t.pairMax, t.pairs},
		{"a|" + email, t.accountMax, t.accounts},
		{"i|" + ip, t.ipMax, t.ips},
	}
	blocked := false
	var longest time.Duration
	for i := range spaces {
		entry := spaces[i]
		w, ok := entry.space[entry.key]
		if !ok {
			continue
		}
		if !now.Before(w.resetAt) {
			delete(entry.space, entry.key)
			continue
		}
		if w.fails >= entry.max {
			blocked = true
			if remaining := w.resetAt.Sub(now); remaining > longest {
				longest = remaining
			}
		}
	}
	return blocked, longest
}

func (t *loginThrottle) take(ip, email string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if blocked, _ := t.blockedLocked(ip, email); blocked {
		return false
	}
	t.failLocked(ip, email)
	return true
}

// fail records one failed attempt in all three key spaces.
func (t *loginThrottle) fail(ip, email string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failLocked(ip, email)
}

func (t *loginThrottle) failLocked(ip, email string) {
	now := t.now()
	t.windowFor(t.pairs, "p|"+ip+"|"+email, now).fails++
	t.windowFor(t.accounts, "a|"+email, now).fails++
	t.windowFor(t.ips, "i|"+ip, now).fails++
}

// reset clears the pair and account windows after a successful login (the
// legitimate user recovers immediately); the IP window is kept — one good
// login from a shared address must not erase a stuffing count.
func (t *loginThrottle) reset(ip, email string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.pairs, "p|"+ip+"|"+email)
	delete(t.accounts, "a|"+email)
}

// windowFor returns the live window for key, creating (and bounding the map)
// when absent or expired.
func (t *loginThrottle) windowFor(space map[string]*loginWindow, key string, now time.Time) *loginWindow {
	if w, ok := space[key]; ok && now.Before(w.resetAt) {
		return w
	}
	if len(space) >= t.maxEntries {
		t.prune(space, now)
		for len(space) >= t.maxEntries {
			t.evictSoonest(space)
		}
	}
	w := &loginWindow{resetAt: now.Add(t.window)}
	space[key] = w
	return w
}

// prune removes expired records.
func (t *loginThrottle) prune(space map[string]*loginWindow, now time.Time) {
	for k, v := range space {
		if !now.Before(v.resetAt) {
			delete(space, k)
		}
	}
}

// evictSoonest removes the record with the earliest reset time — the least
// information loss, and it deterministically bounds memory under attack.
func (t *loginThrottle) evictSoonest(space map[string]*loginWindow) {
	var soonestKey string
	var soonest time.Time
	first := true
	for k, v := range space {
		if first || v.resetAt.Before(soonest) {
			soonestKey, soonest, first = k, v.resetAt, false
		}
	}
	if !first {
		delete(space, soonestKey)
	}
}
