package llm_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/llm"
	"github.com/kpeguero/quantsim/services/ai-insights/internal/narrative"
)

// Every test here points the SDK at a local httptest server through
// option.WithBaseURL, which is its supported seam. No test carries an API key
// and none reaches api.anthropic.com -- the only live call in this step is the
// manual pass.
func clientFor(t *testing.T, h http.HandlerFunc) *llm.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return llm.NewClient("test-key", "claude-opus-5",
		option.WithBaseURL(srv.URL),
		option.WithMaxRetries(0),
	)
}

func TestClient_ReturnsTheModelsRawText(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant",
			"model":"claude-opus-5","stop_reason":"end_turn",
			"content":[{"type":"text","text":"{\"risk\":\"prose\"}"}],
			"usage":{"input_tokens":10,"output_tokens":5}}`))
	})

	got, err := c.Draft(context.Background(), narrative.Prompt{System: "s", User: "u"})
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	if got != `{"risk":"prose"}` {
		t.Errorf("got %q, want the model's text verbatim", got)
	}
}

// The request has to carry what SPEC 2.9 says it does. Asserted on the wire
// rather than on the params struct, because that is what the API actually
// receives.
func TestClient_SendsTheConfiguredModelEffortAndThinking(t *testing.T) {
	var body string
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		body = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","model":"claude-opus-5",
			"stop_reason":"end_turn","content":[{"type":"text","text":"x"}],
			"usage":{"input_tokens":1,"output_tokens":1}}`))
	})

	if _, err := c.Draft(context.Background(), narrative.Prompt{System: "sys", User: "usr"}); err != nil {
		t.Fatalf("Draft: %v", err)
	}
	for _, want := range []string{
		`"model":"claude-opus-5"`,
		`"effort":"low"`,
		`"type":"adaptive"`,
		`"sys"`,
		`"usr"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("request body does not contain %s\nbody: %s", want, body)
		}
	}
	// budget_tokens is removed on this model and would be a 400.
	if strings.Contains(body, "budget_tokens") {
		t.Error("the request carries budget_tokens, which Opus 5 rejects")
	}
}

// A refusal is an HTTP 200 with a stop reason, not an error. Reading the
// content without checking would turn it into an empty draft and burn the
// retry on something a retry cannot fix.
func TestClient_ARefusalIsAnErrorNotAnEmptyDraft(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","model":"claude-opus-5",
			"stop_reason":"refusal","stop_details":{"type":"refusal","explanation":"declined"},
			"content":[],"usage":{"input_tokens":1,"output_tokens":0}}`))
	})

	_, err := c.Draft(context.Background(), narrative.Prompt{})
	if !errors.Is(err, llm.ErrRefused) {
		t.Fatalf("got %v, want ErrRefused", err)
	}
}

func TestClient_MapsFailuresToDistinctReasons(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   error
	}{
		{"rate limited", http.StatusTooManyRequests, llm.ErrRateLimited},
		{"gateway timeout", http.StatusGatewayTimeout, llm.ErrTimeout},
		{"server error", http.StatusInternalServerError, llm.ErrUpstream},
		{"bad request", http.StatusBadRequest, llm.ErrUpstream},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"type":"error","error":{"type":"x","message":"y"}}`))
			})

			_, err := c.Draft(context.Background(), narrative.Prompt{})
			if !errors.Is(err, tc.want) {
				t.Errorf("status %d gave %v, want %v", tc.status, err, tc.want)
			}
		})
	}
}

// A deadline that fired is this service's own budget, not Anthropic being
// down, and reporting it as an upstream failure would blame the wrong party
// in the logs.
func TestClient_ADeadlineIsATimeoutNotAnUpstreamFailure(t *testing.T) {
	// The handler must be released explicitly. Blocking on the request's own
	// context deadlocks the test: httptest's Close waits for outstanding
	// requests, and this one only ends when the server lets it. A function
	// defer runs before any t.Cleanup, so closing release here unblocks the
	// handler in time for Close to return.
	release := make(chan struct{})
	defer close(release)

	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err := c.Draft(ctx, narrative.Prompt{})
	if !errors.Is(err, llm.ErrTimeout) {
		t.Fatalf("got %v, want ErrTimeout", err)
	}
	if errors.Is(err, llm.ErrUpstream) {
		t.Error("a local deadline was reported as an upstream failure")
	}
}

// No retry here: the one retry in this step is narrative.Generate's, and it is
// for a draft that broke the contract rather than a call that failed.
func TestClient_DoesNotRetryAFailedCall(t *testing.T) {
	var calls int
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"x","message":"y"}}`))
	})

	if _, err := c.Draft(context.Background(), narrative.Prompt{}); err == nil {
		t.Fatal("a 500 reported success")
	}
	if calls != 1 {
		t.Errorf("made %d HTTP calls, want 1", calls)
	}
}
