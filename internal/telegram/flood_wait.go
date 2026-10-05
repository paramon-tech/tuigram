package telegram

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/gotd/td/bin"
	gotd "github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// floodGate remembers Telegram's per-method cooldowns for this connection.
// It never queues or replays an RPC: callers retain control of retries, which
// is especially important for mutations and expired command contexts.
type floodGate struct {
	mu       sync.Mutex
	cooldown map[reflect.Type]*floodWaitError
	now      func() time.Time
}

func newFloodGate() *floodGate {
	return &floodGate{cooldown: make(map[reflect.Type]*floodWaitError), now: time.Now}
}

func (g *floodGate) Handle(next tg.Invoker) gotd.InvokeFunc {
	return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Generated request types identify methods without inspecting peer IDs,
		// message contents, credentials, or any other request data.
		method := reflect.TypeOf(input)
		g.mu.Lock()
		if wait := g.cooldown[method]; wait != nil {
			if wait.RetryAfter() > 0 {
				g.mu.Unlock()
				return wait
			}
			delete(g.cooldown, method)
		}
		g.mu.Unlock()

		err := next.Invoke(ctx, input, output)
		duration, flood := floodDuration(err)
		if !flood {
			return err
		}
		wait := &floodWaitError{until: g.now().Add(duration), cause: err, now: g.now}
		g.mu.Lock()
		if previous := g.cooldown[method]; previous != nil && previous.until.After(wait.until) {
			wait = previous
		} else {
			g.cooldown[method] = wait
		}
		g.mu.Unlock()
		// Keep the learned cooldown even if the original caller canceled while
		// its request was in flight. Other requests must still respect it.
		if err := ctx.Err(); err != nil {
			return err
		}
		return wait
	}
}

// RetryAfter is intentionally an interface contract rather than a dependency
// from the TUI to the Telegram package. Unwrap preserves the original RPC error.
type floodWaitError struct {
	until time.Time
	cause error
	now   func() time.Time
}

func (e *floodWaitError) RetryAfter() time.Duration {
	return max(0, e.until.Sub(e.now()))
}

func (e *floodWaitError) Error() string {
	remaining := e.RetryAfter()
	if remaining <= 0 {
		return "Telegram rate limit has expired; retry the operation"
	}
	seconds := int64(remaining / time.Second)
	if remaining%time.Second != 0 {
		seconds++
	}
	return fmt.Sprintf("Telegram rate limit: retry in %d seconds", seconds)
}

func (e *floodWaitError) Unwrap() error { return e.cause }

func floodDuration(err error) (time.Duration, bool) {
	for _, name := range []string{tgerr.ErrFloodWait, tgerr.ErrPremiumFloodWait} {
		if rpc, ok := tgerr.AsType(err, name); ok {
			// Telegram expresses the wait in whole seconds. Add the same one
			// second margin as gotd's FloodWait helper, and avoid overflow on
			// unusually large values instead of accidentally allowing retries.
			const maxSeconds = (int64(1<<63-1) - int64(time.Second)) / int64(time.Second)
			seconds := min(max(int64(rpc.Argument), 0), maxSeconds)
			return time.Duration(seconds)*time.Second + time.Second, true
		}
	}
	return 0, false
}
