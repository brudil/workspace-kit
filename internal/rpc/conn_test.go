package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/brudil/workspace-kit/protocol"
)

// pair is two connections joined end to end.
func pair(a, b Handler) (*Conn, *Conn, func()) {
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	ca, cb := NewConn(ar, aw, a), NewConn(br, bw, b)
	return ca, cb, func() { aw.Close(); bw.Close() }
}

func none(context.Context, string, json.RawMessage) (any, error) { return nil, nil }

func TestCallIsAnswered(t *testing.T) {
	a, _, closeBoth := pair(none, func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method != "double" {
			return nil, MethodNotFound(method)
		}
		var n int
		json.Unmarshal(params, &n) //nolint:errcheck // a test's own params
		return n * 2, nil
	})
	defer closeBoth()

	var got int
	if err := a.Call(context.Background(), "double", 21, &got); err != nil || got != 42 {
		t.Fatalf("double(21) = %d, %v", got, err)
	}
	var re *Error
	if err := a.Call(context.Background(), "nope", nil, nil); !errors.As(err, &re) || re.Code != protocol.CodeMethodNotFound {
		t.Fatalf("an unknown method was answered with %v", err)
	}
}

// A notification sent before a request's answer is handled before the
// call returns: a command's output is all there when the command ends.
func TestNotificationsComeBeforeTheAnswer(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	var b *Conn
	a, b, closeBoth := pair(
		func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			mu.Lock()
			seen = append(seen, method)
			mu.Unlock()
			return nil, nil
		},
		func(_ context.Context, _ string, _ json.RawMessage) (any, error) {
			for range 50 {
				b.Notify("output", nil) //nolint:errcheck // a test
			}
			return "done", nil
		})
	defer closeBoth()

	if err := a.Call(context.Background(), "run", nil, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 50 {
		t.Fatalf("%d of 50 notifications had been handled when the call returned", len(seen))
	}
}

func TestGivingUpCancelsTheHandler(t *testing.T) {
	stopped := make(chan struct{})
	a, _, closeBoth := pair(none, func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	})
	defer closeBoth()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := a.Call(ctx, "wait", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the call returned %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("the handler wasn't told its request was given up on")
	}
}

func TestThenRunsAfterTheAnswer(t *testing.T) {
	ran := make(chan struct{})
	a, _, closeBoth := pair(none, func(context.Context, string, json.RawMessage) (any, error) {
		return Then{Result: "ok", Do: func() { close(ran) }}, nil
	})
	defer closeBoth()

	var got string
	if err := a.Call(context.Background(), "x", nil, &got); err != nil || got != "ok" {
		t.Fatalf("got %q, %v", got, err)
	}
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("what was to be done after the answer wasn't")
	}
}

func TestCallsFailOnceTheOtherSideIsGone(t *testing.T) {
	a, _, closeBoth := pair(none, none)
	closeBoth()
	<-a.Done()
	if err := a.Call(context.Background(), "x", nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("a call on a closed connection returned %v", err)
	}
}
