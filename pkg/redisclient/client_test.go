package redisclient

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// hungRedis accepts connections and reads from them but never replies: what a
// paused or wedged Redis looks like. A closed port is not the same test,
// because a refused connection fails in microseconds and every client looks
// bounded against it.
func hungRedis(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
			go func() {
				buf := make([]byte, 4096)
				for {
					if _, err := conn.Read(buf); err != nil {
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func newHungClient(t *testing.T) *redis.Client {
	t.Helper()
	opts, err := redis.ParseURL("redis://" + hungRedis(t))
	if err != nil {
		t.Fatalf("ParseURL: %v", err)
	}
	c := New(opts)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// A client built the ordinary way waits go-redis's 3s ReadTimeout here, with
// or without a deadline. Both limits sit well below that and well above the
// bound each case is meant to hit.
func TestNew_BoundsACallToAHungRedis(t *testing.T) {
	t.Run("the caller's deadline is honoured", func(t *testing.T) {
		c := newHungClient(t)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		start := time.Now()
		err := c.Get(ctx, "k").Err()
		if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
			t.Errorf("took %v with a 100ms deadline: the client ignored the context", elapsed)
		}
		if err == nil {
			t.Error("a hung Redis reported success")
		}
	})

	t.Run("a call with no deadline is still bounded", func(t *testing.T) {
		c := newHungClient(t)

		start := time.Now()
		err := c.Get(context.Background(), "k").Err()
		if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
			t.Errorf("took %v with no deadline, want about %v", elapsed, Timeout)
		}
		if err == nil {
			t.Error("a hung Redis reported success")
		}
	})
}

func TestNew_KeepsTimeoutsAlreadySet(t *testing.T) {
	opts, err := redis.ParseURL("redis://127.0.0.1:6379?read_timeout=2s&write_timeout=3s&dial_timeout=4s")
	if err != nil {
		t.Fatalf("ParseURL: %v", err)
	}
	c := New(opts)
	defer c.Close()

	got := c.Options()
	if got.ReadTimeout != 2*time.Second || got.WriteTimeout != 3*time.Second || got.DialTimeout != 4*time.Second {
		t.Errorf("timeouts = read %v, write %v, dial %v; want 2s, 3s, 4s from the URL",
			got.ReadTimeout, got.WriteTimeout, got.DialTimeout)
	}
	if !got.ContextTimeoutEnabled {
		t.Error("ContextTimeoutEnabled is false")
	}
}

// A stalled write or dial cannot be produced reliably on loopback, so these two
// defaults are checked as configuration rather than against a hung server.
func TestNew_FillsUnsetTimeouts(t *testing.T) {
	c := New(&redis.Options{Addr: "127.0.0.1:6379"})
	defer c.Close()

	got := c.Options()
	if got.ReadTimeout != Timeout || got.WriteTimeout != Timeout || got.DialTimeout != Timeout {
		t.Errorf("timeouts = read %v, write %v, dial %v; want %v each",
			got.ReadTimeout, got.WriteTimeout, got.DialTimeout, Timeout)
	}
}
