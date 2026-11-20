package transport

// Unit tests for the login throttle: injected-clock window boundaries, the
// three key spaces (pair/account/IP), lockout-DoS resistance, hard memory
// bounds, and concurrency safety (exercised under -race by the suite).

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestLoginThrottleUsesInjectedClockAndExpiresAtBoundary(t *testing.T) {
	now := time.Date(2026, time.November, 21, 12, 0, 0, 0, time.UTC)
	throttle := newLoginThrottle(1, time.Minute)
	throttle.now = func() time.Time { return now }
	throttle.fail("1.2.3.4", "victim@test.example")

	// The injected clock drives EVERY read: one full window remains.
	blocked, retry := throttle.blocked("1.2.3.4", "victim@test.example")
	if !blocked || retry != time.Minute {
		t.Fatalf("blocked=%v retry=%s, want true and 1m", blocked, retry)
	}

	// Exactly halfway: 30s remain — the pair key is the strictest budget.
	now = now.Add(30 * time.Second)
	blocked, retry = throttle.blocked("1.2.3.4", "victim@test.example")
	if !blocked || retry != 30*time.Second {
		t.Fatalf("blocked=%v retry=%s, want true and 30s", blocked, retry)
	}

	// One nanosecond before the boundary is still blocked.
	now = now.Add(30*time.Second - time.Nanosecond)
	if blocked, _ = throttle.blocked("1.2.3.4", "victim@test.example"); !blocked {
		t.Fatal("still inside the window: want blocked")
	}
	// At the exact boundary the window expires (resetAt is exclusive).
	now = now.Add(time.Nanosecond)
	blocked, retry = throttle.blocked("1.2.3.4", "victim@test.example")
	if blocked || retry != 0 {
		t.Fatalf("at window boundary: blocked=%v retry=%s, want false and 0", blocked, retry)
	}
}

func TestLoginThrottlePerAccountAcrossIPs(t *testing.T) {
	now := time.Date(2026, time.November, 21, 12, 0, 0, 0, time.UTC)
	throttle := newLoginThrottle(10, 5*time.Minute) // accountMax = 30
	throttle.now = func() time.Time { return now }

	// 30 distributed failures against ONE account from 30 distinct
	// addresses: each pair bucket holds 1 fail (nowhere near pairMax), the
	// account bucket fills to its cap.
	for i := 1; i <= 30; i++ {
		ip := fmt.Sprintf("10.0.%d.%d", i/256, i%256)
		if blocked, _ := throttle.blocked(ip, "target@test.example"); blocked {
			t.Fatalf("blocked after %d distributed failures: too strict", i)
		}
		throttle.fail(ip, "target@test.example")
	}
	// 30th distributed failure tips the ACCOUNT window.
	if blocked, retry := throttle.blocked("10.9.9.9", "target@test.example"); !blocked || retry <= 0 {
		t.Fatalf("account window did not engage: blocked=%v retry=%s", blocked, retry)
	}
	// …for every address, including a brand-new one.
	if blocked, _ := throttle.blocked("192.0.2.1", "target@test.example"); !blocked {
		t.Fatal("account window must block from a fresh address too")
	}
	// A DIFFERENT account from the same addresses is unaffected.
	if blocked, _ := throttle.blocked("10.0.0.1", "other@test.example"); blocked {
		t.Fatal("account window leaked onto another account")
	}
}

func TestLoginThrottlePerIPAcrossAccounts(t *testing.T) {
	now := time.Date(2026, time.November, 21, 12, 0, 0, 0, time.UTC)
	throttle := newLoginThrottle(10, 5*time.Minute) // ipMax = 100
	throttle.now = func() time.Time { return now }

	// One address spraying 100 distinct accounts: each pair holds 1 fail,
	// the IP window fills.
	for i := 1; i <= 100; i++ {
		email := fmt.Sprintf("spray-%03d@test.example", i)
		if blocked, _ := throttle.blocked("198.51.100.7", email); blocked {
			t.Fatalf("blocked after %d sprayed accounts: too strict", i)
		}
		throttle.fail("198.51.100.7", email)
	}
	if blocked, retry := throttle.blocked("198.51.100.7", "brand-new@test.example"); !blocked || retry <= 0 {
		t.Fatalf("IP window did not engage for a fresh account: blocked=%v retry=%s", blocked, retry)
	}
	// Another address is unaffected.
	if blocked, _ := throttle.blocked("198.51.100.8", "brand-new@test.example"); blocked {
		t.Fatal("IP window leaked onto another address")
	}
}

func TestLoginThrottleSingleAttackerCannotLockOutVictim(t *testing.T) {
	now := time.Date(2026, time.November, 21, 12, 0, 0, 0, time.UTC)
	throttle := newLoginThrottle(10, 5*time.Minute)
	throttle.now = func() time.Time { return now }

	// An attacker exhausts their own PAIR bucket against the victim account.
	for range 10 {
		throttle.fail("203.0.113.66", "victim@test.example")
	}
	if blocked, _ := throttle.blocked("203.0.113.66", "victim@test.example"); !blocked {
		t.Fatal("attacker pair should be blocked")
	}
	// The victim signing in from their OWN address is untouched (account
	// window holds 10 of 30): no cheap lockout DoS.
	if blocked, _ := throttle.blocked("192.0.2.99", "victim@test.example"); blocked {
		t.Fatal("victim blocked by a single attacker address")
	}
	// A successful login clears pair+account; the attacker's IP window keeps
	// counting (shared-address stuffing survives a legitimate login).
	for range 5 { // victim fails a few times from their own address
		throttle.fail("192.0.2.99", "victim@test.example")
	}
	throttle.reset("192.0.2.99", "victim@test.example")
	if blocked, _ := throttle.blocked("192.0.2.99", "victim@test.example"); blocked {
		t.Fatal("reset did not clear the victim's pair window")
	}
	if blocked, _ := throttle.blocked("203.0.113.66", "victim@test.example"); !blocked {
		t.Fatal("reset leaked onto the attacker's pair window")
	}
}

func TestLoginThrottleBlockedReportsLongestWindow(t *testing.T) {
	now := time.Date(2026, time.November, 21, 12, 0, 0, 0, time.UTC)
	throttle := newLoginThrottle(1, 5*time.Minute)
	throttle.now = func() time.Time { return now }

	// Pair window trips at t0 (expires t0+5m).
	throttle.fail("203.0.113.1", "a@test.example")
	now = now.Add(time.Minute)
	// Account window trips later (expires later) via a different address.
	throttle.fail("203.0.113.2", "a@test.example")
	throttle.fail("203.0.113.3", "a@test.example")
	throttle.fail("203.0.113.4", "a@test.example") // accountMax = 3 tripped

	blocked, retry := throttle.blocked("203.0.113.1", "a@test.example")
	if !blocked {
		t.Fatal("want blocked")
	}
	// The account window (4 minutes left) outlives the pair window
	// (3 minutes left): Retry-After must use the LONGER duration.
	if want := 4 * time.Minute; retry != want {
		t.Fatalf("retry=%s, want the longest window %s", retry, want)
	}
}

func TestLoginThrottleMemoryIsHardBounded(t *testing.T) {
	now := time.Date(2026, time.November, 21, 12, 0, 0, 0, time.UTC)
	throttle := newLoginThrottle(10, time.Hour)
	throttle.now = func() time.Time { return now }
	throttle.maxEntries = 100 // shrink the cap for a fast test

	// High-cardinality spray: 3× the cap in distinct keys.
	for i := 0; i < 300; i++ {
		throttle.fail(fmt.Sprintf("198.51.100.%d", i%256)+fmt.Sprintf("-%d", i), fmt.Sprintf("h-%d@test.example", i))
	}
	for _, space := range []map[string]*loginWindow{throttle.pairs, throttle.accounts, throttle.ips} {
		if len(space) > throttle.maxEntries {
			t.Fatalf("map grew to %d entries, cap %d", len(space), throttle.maxEntries)
		}
	}
	// The limiter keeps functioning after eviction pressure.
	for range 5 {
		throttle.fail("198.51.100.1", "final@test.example")
	}
	if blocked, _ := throttle.blocked("198.51.100.1", "final@test.example"); blocked {
		t.Fatal("post-eviction tracking broken: blocked too early")
	}
}

func TestLoginThrottlePrunesExpiredUnderPressure(t *testing.T) {
	now := time.Date(2026, time.November, 21, 12, 0, 0, 0, time.UTC)
	throttle := newLoginThrottle(10, time.Minute)
	throttle.now = func() time.Time { return now }
	throttle.maxEntries = 10

	// Fill the account space with windows that then expire.
	for i := 0; i < 10; i++ {
		throttle.fail("192.0.2.1", fmt.Sprintf("old-%d@test.example", i))
	}
	now = now.Add(2 * time.Minute) // all windows expired
	for i := 0; i < 20; i++ {
		throttle.fail("192.0.2.2", fmt.Sprintf("new-%d@test.example", i))
	}
	// Expired records were pruned rather than evicting live ones; the map
	// never exceeded the cap.
	if len(throttle.accounts) > throttle.maxEntries {
		t.Fatalf("account map = %d entries, cap %d", len(throttle.accounts), throttle.maxEntries)
	}
	if blocked, _ := throttle.blocked("192.0.2.2", "new-19@test.example"); blocked {
		t.Fatal("fresh window wrongly blocked after pruning")
	}
}

func TestLoginThrottleConcurrentUse(t *testing.T) {
	now := time.Date(2026, time.November, 21, 12, 0, 0, 0, time.UTC)
	throttle := newLoginThrottle(50, time.Minute)
	var mu sync.Mutex // guards the injected clock only
	throttle.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}

	var wg sync.WaitGroup
	for worker := range 16 {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				ip := fmt.Sprintf("10.%d.%d.%d", worker, i/256, i%256)
				email := fmt.Sprintf("c-%d-%d@test.example", worker, i)
				throttle.fail(ip, email)
				throttle.blocked(ip, email)
				if i%10 == 0 {
					throttle.reset(ip, email)
				}
			}
			// Contended single key: 16 workers hammer the same pair.
			for range 200 {
				throttle.fail("10.9.9.9", "hot@test.example")
				throttle.blocked("10.9.9.9", "hot@test.example")
			}
		}(worker)
	}
	wg.Wait()
	if blocked, _ := throttle.blocked("10.9.9.9", "hot@test.example"); !blocked {
		t.Fatal("contended key did not reach its budget (lost updates?)")
	}
}
