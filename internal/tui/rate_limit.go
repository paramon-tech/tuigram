package tui

import (
	"errors"
	"time"
)

func retryAfter(err error) time.Duration {
	var limited interface{ RetryAfter() time.Duration }
	if errors.As(err, &limited) {
		return limited.RetryAfter()
	}
	return 0
}

// Keep scheduled refreshes quiet for the server's requested interval. The
// backend independently rejects rate-limited manual requests without sending
// them or replaying mutations once the wait ends.
func (m *Model) deferPolling(err error) {
	if delay := retryAfter(err); delay > 0 {
		until := time.Now().Add(delay)
		if until.After(m.pollRetryAt) {
			m.pollRetryAt = until
		}
	}
}
