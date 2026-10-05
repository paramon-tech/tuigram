package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	gotd "github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

type floodTestClock struct{ elapsed atomic.Int64 }

func (c *floodTestClock) Now() time.Time          { return time.Unix(1000, c.elapsed.Load()) }
func (c *floodTestClock) Advance(d time.Duration) { c.elapsed.Add(int64(d)) }

func testFloodGate() (*floodGate, *floodTestClock) {
	g, clock := newFloodGate(), &floodTestClock{}
	g.now = clock.Now
	return g, clock
}

func requireFloodDelay(t *testing.T, err error, want time.Duration) {
	t.Helper()
	var rateLimit interface{ RetryAfter() time.Duration }
	if !errors.As(err, &rateLimit) || rateLimit.RetryAfter() != want {
		t.Fatalf("wanted rate limit %s, got %v", want, err)
	}
}

func TestFloodGateHonorsCooldownWithoutNetworkOrAutomaticRetry(t *testing.T) {
	g, clock := testFloodGate()
	var calls atomic.Int32
	original := tgerr.New(420, "FLOOD_WAIT_10")
	invoke := g.Handle(gotd.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		if calls.Add(1) == 1 {
			return fmt.Errorf("invoke failed: %w", original)
		}
		return nil
	}))
	err := invoke(context.Background(), &tg.MessagesGetHistoryRequest{Limit: 100}, nil)
	requireFloodDelay(t, err, 11*time.Second)
	if calls.Load() != 1 || !errors.Is(err, original) || !tgerr.Is(err, tgerr.ErrFloodWait) {
		t.Fatalf("request replayed or original RPC error lost: calls=%d error=%v", calls.Load(), err)
	}
	clock.Advance(5500 * time.Millisecond)
	requireFloodDelay(t, fmt.Errorf("history: %w", err), 5500*time.Millisecond)
	if !strings.Contains(err.Error(), "retry in 6 seconds") {
		t.Fatalf("remaining time not rounded upward: %v", err)
	}
	err = invoke(context.Background(), &tg.MessagesGetHistoryRequest{Limit: 20}, nil)
	requireFloodDelay(t, err, 5500*time.Millisecond)
	if calls.Load() != 1 {
		t.Fatal("another history request reached Telegram during the cooldown")
	}
	// A different method remains usable while history is cooling down.
	if err := invoke(context.Background(), &tg.ContactsGetContactsRequest{}, nil); err != nil || calls.Load() != 2 {
		t.Fatalf("unrelated method blocked: %v", err)
	}
	clock.Advance(5500 * time.Millisecond)
	if err := invoke(context.Background(), &tg.MessagesGetHistoryRequest{}, nil); err != nil || calls.Load() != 3 {
		t.Fatalf("history did not resume at expiry: %v", err)
	}
}

func TestFloodGateNeverReplaysMutations(t *testing.T) {
	g, clock := testFloodGate()
	var calls atomic.Int32
	invoke := g.Handle(gotd.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		calls.Add(1)
		return tgerr.New(420, "FLOOD_WAIT_1")
	}))
	request := &tg.MessagesSendMessageRequest{RandomID: 123, Message: "send once"}
	requireFloodDelay(t, invoke(context.Background(), request, nil), 2*time.Second)
	clock.Advance(3 * time.Second)
	if calls.Load() != 1 {
		t.Fatal("mutation was automatically retried after the cooldown")
	}
	// Only a fresh caller action is allowed to try again.
	requireFloodDelay(t, invoke(context.Background(), request, nil), 2*time.Second)
	if calls.Load() != 2 {
		t.Fatalf("explicit retry invoked %d total requests", calls.Load())
	}
}

func TestFloodGateCancellationAndSharedInvokerState(t *testing.T) {
	g, _ := testFloodGate()
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	invoke := g.Handle(gotd.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		calls.Add(1)
		cancel()
		return tgerr.New(420, "FLOOD_PREMIUM_WAIT_20")
	}))
	err := invoke(ctx, &tg.UploadGetFileRequest{}, nil)
	if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatalf("in-flight cancellation lost: %v", err)
	}
	if err := invoke(ctx, &tg.ContactsGetContactsRequest{}, nil); !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatalf("canceled request reached Telegram: %v", err)
	}
	// The flood response is remembered even though its caller canceled, and
	// separate invoker wrappers share the same gate.
	other := g.Handle(gotd.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		calls.Add(1)
		return nil
	}))
	requireFloodDelay(t, other(context.Background(), &tg.UploadGetFileRequest{}, nil), 21*time.Second)
	if calls.Load() != 1 {
		t.Fatal("canceled flood response was forgotten by another invoker")
	}
	if err := other(ctx, &tg.UploadGetFileRequest{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cooldown overrode caller cancellation: %v", err)
	}
}

func TestFloodGateConcurrentResponsesKeepLongestDeadline(t *testing.T) {
	for _, first := range []string{"long", "short"} {
		t.Run(first+" response first", func(t *testing.T) {
			g, _ := testFloodGate()
			entered := make(chan struct{}, 2)
			longRelease, shortRelease := make(chan struct{}), make(chan struct{})
			invoke := g.Handle(gotd.InvokeFunc(func(_ context.Context, input bin.Encoder, _ bin.Decoder) error {
				entered <- struct{}{}
				if input.(*tg.MessagesGetHistoryRequest).Limit == 1 {
					<-longRelease
					return tgerr.New(420, "FLOOD_WAIT_60")
				}
				<-shortRelease
				return tgerr.New(420, "FLOOD_WAIT_2")
			}))
			longResult, shortResult := make(chan error, 1), make(chan error, 1)
			go func() { longResult <- invoke(context.Background(), &tg.MessagesGetHistoryRequest{Limit: 1}, nil) }()
			go func() { shortResult <- invoke(context.Background(), &tg.MessagesGetHistoryRequest{Limit: 2}, nil) }()
			<-entered
			<-entered
			if first == "long" {
				close(longRelease)
				requireFloodDelay(t, <-longResult, 61*time.Second)
				close(shortRelease)
				requireFloodDelay(t, <-shortResult, 61*time.Second)
			} else {
				close(shortRelease)
				requireFloodDelay(t, <-shortResult, 3*time.Second)
				close(longRelease)
				requireFloodDelay(t, <-longResult, 61*time.Second)
			}
			requireFloodDelay(t, invoke(context.Background(), &tg.MessagesGetHistoryRequest{}, nil), 61*time.Second)
		})
	}
}

func TestFloodGatePassesThroughOtherFailures(t *testing.T) {
	for _, original := range []error{errors.New("network disconnected"), tgerr.New(400, "MESSAGE_ID_INVALID"), tgerr.New(420, "SLOWMODE_WAIT_30")} {
		g, _ := testFloodGate()
		var calls int
		invoke := g.Handle(gotd.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
			calls++
			return original
		}))
		for range 2 {
			if got := invoke(context.Background(), &tg.MessagesSendMessageRequest{}, nil); got != original {
				t.Fatalf("non-flood error changed: %v", got)
			}
		}
		if calls != 2 {
			t.Fatal("non-flood error unexpectedly installed a method cooldown")
		}
	}
}

func TestFloodDurationBounds(t *testing.T) {
	for _, tc := range []struct {
		seconds int
		want    time.Duration
	}{
		{seconds: 0, want: time.Second},
		{seconds: -1, want: time.Second},
		{seconds: 30, want: 31 * time.Second},
	} {
		duration, ok := floodDuration(&tgerr.Error{Code: 420, Type: tgerr.ErrFloodWait, Argument: tc.seconds})
		if !ok || duration != tc.want {
			t.Fatalf("duration for %d seconds = %s, %v", tc.seconds, duration, ok)
		}
	}
	duration, ok := floodDuration(&tgerr.Error{Code: 420, Type: tgerr.ErrFloodWait, Argument: int(^uint(0) >> 1)})
	if !ok || duration <= 0 {
		t.Fatalf("large wait overflowed: %s", duration)
	}
}
