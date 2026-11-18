package plugin

import "time"

// SetRetryAfter sets the wait after a sync's first failure, for a test,
// and returns what puts it back.
func SetRetryAfter(d time.Duration) (restore func()) {
	was := retryAfter
	retryAfter = d
	return func() { retryAfter = was }
}

// SetClock gives a plugin a wall clock of a test's, and how often its
// resting syncs look at it. It's called as the plugin's declared.
func SetClock(p *Plugin, now func() time.Time, recheck time.Duration) {
	p.now, p.recheck = now, recheck
}
