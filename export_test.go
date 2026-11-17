package plugin

import "time"

// SetRetryAfter sets the wait after a sync's first failure, for a test,
// and returns what puts it back.
func SetRetryAfter(d time.Duration) (restore func()) {
	was := retryAfter
	retryAfter = d
	return func() { retryAfter = was }
}
