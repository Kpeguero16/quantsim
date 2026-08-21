package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/kpeguero/quantsim/services/ai-insights/internal/narrative"
)

var _ narrative.Generator = (*Client)(nil)

const (
	// requestTimeout bounds one generation (SPEC.md §2.11).
	//
	// Comfortably inside the gateway's 30s ResponseHeaderTimeout. It does not
	// interact with the 5s requestTimeout in internal/client, which bounds the
	// calls to trading-engine and market-data on a different client entirely.
	requestTimeout = 20 * time.Second

	// maxTokens is sized for three short paragraphs with room to spare. Small
	// enough that a non-streaming call is never near an HTTP timeout.
	maxTokens = 2000
)

// Failure modes a caller may want to tell apart. They exist so the endpoint's
// `reason` can say which kind of unavailable it is (SPEC.md §2.11), and each
// wraps the SDK's own error rather than replacing it.
var (
	ErrRateLimited = errors.New("llm: rate limited")
	ErrTimeout     = errors.New("llm: generation timed out")
	ErrRefused     = errors.New("llm: the model declined to answer")
	ErrUpstream    = errors.New("llm: upstream failure")
)

// Client generates narrative drafts with the Claude API.
//
// It implements narrative.Generator and returns the model's RAW text. It does
// not validate, substitute or render, and it does not know that it must not
// produce figures -- that whole guarantee lives on the other side of the
// interface (plan D3), which is what stops a change here from weakening it.
type Client struct {
	api   anthropic.Client
	model string
}

// NewClient builds a client for one API key. Constructed once at boot and
// reused, like the Redis client.
func NewClient(apiKey, model string, opts ...option.RequestOption) *Client {
	opts = append([]option.RequestOption{option.WithAPIKey(apiKey)}, opts...)
	return &Client{api: anthropic.NewClient(opts...), model: model}
}

// Draft asks for one narrative and returns whatever text came back.
//
// No retry. The single retry in this step is narrative.Generate's, and it is
// for a draft that broke the contract rather than for a call that failed --
// retrying a timeout doubles the wait before the same failure, and the
// endpoint has a budget to keep (SPEC.md §2.11).
func (c *Client) Draft(ctx context.Context, p narrative.Prompt) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	resp, err := c.api.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: maxTokens,
		System: []anthropic.TextBlockParam{{
			Text: p.System,
			// The system prompt is byte-identical across requests, so it is
			// the one part of this call that can cache. Cheap to ask for; the
			// content-hash cache is what actually bounds cost.
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
		// Adaptive is the only supported on-mode on current models, and is
		// what Opus 5 runs by default. Effort low: this is constrained
		// phrasing of a supplied object, not analysis.
		Thinking:     anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{}},
		OutputConfig: anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffortLow},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(p.User)),
		},
	})
	if err != nil {
		return "", classify(ctx, err)
	}

	// A refusal is an HTTP 200 with a stop reason, not an error, so it has to
	// be checked before the content is read -- otherwise it reads as an empty
	// draft and burns the retry.
	if resp.StopReason == anthropic.StopReasonRefusal {
		return "", fmt.Errorf("%w: %s", ErrRefused, resp.StopDetails.Explanation)
	}

	var text string
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			text += t.Text
		}
	}
	return text, nil
}

// classify turns an SDK error into one this service's handler can name.
//
// The context check comes first: a deadline that fired is reported by the SDK
// as a transport error, and calling that "upstream unavailable" would blame
// Anthropic for this service's own budget.
func classify(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%w: %v", ErrTimeout, err)
	}

	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusTooManyRequests:
			return fmt.Errorf("%w: %v", ErrRateLimited, err)
		case http.StatusRequestTimeout, http.StatusGatewayTimeout:
			return fmt.Errorf("%w: %v", ErrTimeout, err)
		}
	}
	return fmt.Errorf("%w: %v", ErrUpstream, err)
}
