// Package marginfuse is the server-side SDK for MarginFuse: profitability
// guardrails for AI SaaS. Connect revenue to per-request AI cost, see gross
// margin per customer, and stop loss-making requests before they run.
//
// Reliability contract: this SDK never panics into application code and never
// blocks a request on MarginFuse availability. Decide fails open to
// ActionAllow on any timeout or error; Track and Acknowledge retry in the
// background and surface problems only through Config.OnError.
//
// Server side only: it carries a secret API key.
package marginfuse

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	defaultBaseURL = "https://api.marginfuse.com"
	defaultTimeout = 1500 * time.Millisecond
	// Identify is not on the request hot path - it runs at sign-in - so it
	// gets room to answer rather than the decision budget.
	identifyTimeout = 5 * time.Second
	trackRetries    = 3
	userAgent       = "marginfuse-go/" + Version
)

// Config configures a Client. Every field except APIKey has a usable zero
// value.
type Config struct {
	// APIKey is your project API key. Required.
	APIKey string

	// BaseURL points at your own deployment in development.
	BaseURL string

	// Timeout is how long Decide waits before failing open. Default 1.5s.
	Timeout time.Duration

	// OnError receives transport failures the SDK swallowed. Without it they
	// are silent by design: this SDK is in your request path and must not
	// become your outage.
	OnError func(err error, context string)

	// HTTPClient replaces the default. Useful for proxies and test doubles.
	HTTPClient *http.Client
}

// Client is safe for concurrent use.
type Client struct {
	apiKey  string
	baseURL string
	timeout time.Duration
	onError func(error, string)
	http    *http.Client

	wg     sync.WaitGroup
	closed chan struct{}
	once   sync.Once
}

// New returns a Client. It returns an error only for a missing API key,
// because that is a programming mistake rather than a runtime condition.
func New(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("marginfuse: APIKey is required")
	}
	c := &Client{
		apiKey:  cfg.APIKey,
		baseURL: cfg.BaseURL,
		timeout: cfg.Timeout,
		onError: cfg.OnError,
		http:    cfg.HTTPClient,
		closed:  make(chan struct{}),
	}
	if c.baseURL == "" {
		c.baseURL = defaultBaseURL
	}
	for len(c.baseURL) > 0 && c.baseURL[len(c.baseURL)-1] == '/' {
		c.baseURL = c.baseURL[:len(c.baseURL)-1]
	}
	if c.timeout <= 0 {
		c.timeout = defaultTimeout
	}
	if c.http == nil {
		c.http = &http.Client{}
	}
	return c, nil
}

// Decide asks whether the next call should run. It always returns a verdict.
//
// There is no error return on purpose. A failed decision is not a condition
// the caller should branch on: it is an allow with Degraded set, because
// MarginFuse being unreachable must never become your outage. Transport
// failures go to Config.OnError.
func (c *Client) Decide(ctx context.Context, p DecideParams) Decision {
	failOpen := func(reason string) Decision {
		return Decision{
			Action:         ActionAllow,
			Model:          p.Model,
			Provider:       p.Provider,
			Degraded:       true,
			DegradedReason: reason,
		}
	}

	body := map[string]any{
		"customerId": p.CustomerID,
		"provider":   p.Provider,
		"model":      p.Model,
	}
	if p.Plan != "" {
		body["plan"] = p.Plan
	}
	if p.Feature != "" {
		body["feature"] = p.Feature
	}
	if p.ExpectedUsage != (Usage{}) {
		body["expectedUsage"] = p.ExpectedUsage
	}

	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	status, raw, err := c.post(reqCtx, "/v1/decisions", body)
	if err != nil {
		c.report(err, "decide")
		if errors.Is(err, context.DeadlineExceeded) {
			return failOpen("timeout")
		}
		return failOpen("unreachable")
	}
	if status < 200 || status >= 300 {
		c.report(fmt.Errorf("decide: HTTP %d", status), "decide")
		return failOpen(fmt.Sprintf("server responded %d", status))
	}

	var d Decision
	if err := json.Unmarshal(raw, &d); err != nil {
		c.report(fmt.Errorf("decide: %w", err), "decide")
		return failOpen("unreadable response")
	}
	if d.Action == "" {
		d.Action = ActionAllow
	}
	if d.Model == "" {
		d.Model = p.Model
	}
	if d.Provider == "" {
		d.Provider = p.Provider
	}
	return d
}

// Track reports a call that already happened. It returns immediately and
// sends in the background with retries.
//
// Call Flush before a process exits, or the last events go with it.
func (c *Client) Track(p TrackParams) {
	when := p.OccurredAt
	if when.IsZero() {
		when = time.Now().UTC()
	}
	outcome := p.Outcome
	if outcome == "" {
		outcome = OutcomeSuccess
	}
	eventID := p.EventID
	if eventID == "" {
		eventID = newEventID()
	}

	event := map[string]any{
		"eventId":    eventID,
		"customerId": p.CustomerID,
		"provider":   p.Provider,
		"model":      p.Model,
		"usage":      p.Usage,
		"occurredAt": when.UTC().Format(time.RFC3339Nano),
		"outcome":    string(outcome),
	}
	for key, value := range map[string]string{
		"plan":            p.Plan,
		"feature":         p.Feature,
		"requestedModel":  p.RequestedModel,
		"costUsd":         p.CostUSD,
		"decisionId":      p.DecisionID,
		"retryOfEventId":  p.RetryOfEventID,
		"correctsEventId": p.CorrectsEventID,
	} {
		if value != "" {
			event[key] = value
		}
	}

	c.background(func(ctx context.Context) {
		var last error
		for attempt := 0; attempt < trackRetries; attempt++ {
			status, raw, err := c.post(ctx, "/v1/events", map[string]any{
				"events": []any{event},
			})
			if err == nil && status >= 200 && status < 300 {
				return
			}
			if err == nil && status >= 400 && status < 500 && status != 429 {
				// A malformed event is malformed on every attempt.
				c.report(fmt.Errorf("track: HTTP %d %s", status, snippet(raw)), "track")
				return
			}
			if err != nil {
				last = err
			} else {
				last = fmt.Errorf("track: HTTP %d", status)
			}
			select {
			case <-time.After(time.Duration(250*(1<<attempt)) * time.Millisecond):
			case <-ctx.Done():
				c.report(ctx.Err(), "track")
				return
			}
		}
		if last != nil {
			c.report(last, "track")
		}
	})
}

// Identify tells MarginFuse who a customer is and what plan they are on.
//
// Plan is the key of a plan you declared in MarginFuse Settings, not a Stripe
// price id. MarginFuse derives that customer's revenue from the plan's price
// for every cycle, which is what makes margin per customer and margin policies
// work with no revenue source connected. Those figures are labeled as a
// declared price wherever they appear, because nobody confirmed collection.
//
// Safe to call on every sign-in: sending the plan the customer is already on
// changes nothing. Sending a different one ends the current cycle at that
// moment and prorates what accrued.
//
// This is the one call that returns an error, and the only one that should.
// Decide fails open and Track retries, because both have a safe default;
// "I could not record what this customer pays" has none, and a wrong plan is a
// wrong margin. The error also goes to Config.OnError.
func (c *Client) Identify(ctx context.Context, p IdentifyParams) (Identity, error) {
	body := map[string]any{"customerId": p.CustomerID}
	if p.Plan != "" {
		body["plan"] = p.Plan
	}
	if p.ClearPlan {
		body["clearPlan"] = true
	}
	if !p.PeriodStart.IsZero() {
		body["periodStart"] = p.PeriodStart.UTC().Format(time.RFC3339Nano)
	}
	for key, value := range map[string]string{"name": p.Name, "email": p.Email} {
		if value != "" {
			body[key] = value
		}
	}
	if len(p.Metadata) > 0 {
		body["metadata"] = p.Metadata
	}

	reqCtx, cancel := context.WithTimeout(ctx, identifyTimeout)
	defer cancel()

	status, raw, err := c.post(reqCtx, "/v1/identify", body)
	if err != nil {
		c.report(err, "identify")
		return Identity{}, err
	}
	if status < 200 || status >= 300 {
		err := fmt.Errorf("identify: HTTP %d %s", status, snippet(raw))
		c.report(err, "identify")
		return Identity{}, err
	}
	var id Identity
	if err := json.Unmarshal(raw, &id); err != nil {
		err = fmt.Errorf("identify: %w", err)
		c.report(err, "identify")
		return Identity{}, err
	}
	return id, nil
}

// TrackAndWait is Track for jobs and scripts that must not exit early.
func (c *Client) TrackAndWait(ctx context.Context, p TrackParams) {
	c.Track(p)
	c.Flush(ctx)
}

// Acknowledge tells MarginFuse what your application did with a decision.
func (c *Client) Acknowledge(decisionID string, ack Acknowledgment) {
	c.background(func(ctx context.Context) {
		status, _, err := c.post(ctx,
			"/v1/decisions/"+decisionID+"/ack",
			map[string]any{"acknowledgment": string(ack)})
		if err != nil {
			c.report(err, "acknowledge")
			return
		}
		if status < 200 || status >= 300 {
			c.report(fmt.Errorf("ack: HTTP %d", status), "acknowledge")
		}
	})
}

// Guard runs the whole loop: ask, run, report, acknowledge.
//
// run receives the decision and must return what the call consumed. Use
// decision.Model: a downgrade verdict changes it.
//
// It takes a callback rather than returning a decision for you to act on,
// because enforcement must not depend on the caller remembering to check
// anything. When the verdict is block, run is never invoked.
//
// An error from run is returned unchanged: your error handling owns provider
// failures. The attempt is recorded before it is returned, because the
// provider may still have charged for it.
func (c *Client) Guard(
	ctx context.Context,
	p DecideParams,
	run func(context.Context, Decision) (ProviderCall, error),
) (GuardOutcome, error) {
	decision := c.Decide(ctx, p)

	// Enforcement depends on the ACTION alone. A missing ID costs an
	// acknowledgment; it must never turn a block into a provider call.
	switch decision.Action {
	case ActionBlock:
		if decision.ID != "" {
			c.Acknowledge(decision.ID, AckBlockedBeforeProviderCall)
		}
		return GuardOutcome{Kind: GuardBlocked, Decision: decision}, nil
	case ActionTopupRequired:
		if decision.ID != "" {
			c.Acknowledge(decision.ID, AckPresentedTopup)
		}
		return GuardOutcome{Kind: GuardTopupRequired, Decision: decision}, nil
	}

	modelUsed := p.Model
	if decision.Action == ActionDowngrade {
		modelUsed = decision.Model
	}

	call, err := run(ctx, decision)
	if err != nil {
		c.Track(TrackParams{
			CustomerID:     p.CustomerID,
			Feature:        p.Feature,
			Provider:       p.Provider,
			Model:          modelUsed,
			RequestedModel: p.Model,
			Outcome:        OutcomeProviderError,
			DecisionID:     decision.ID,
		})
		if decision.ID != "" {
			c.Acknowledge(decision.ID, AckProceededAsRequested)
		}
		return GuardOutcome{Kind: GuardCompleted, Decision: decision}, err
	}

	outcome := call.Outcome
	if outcome == "" {
		outcome = OutcomeSuccess
	}
	c.Track(TrackParams{
		CustomerID:     p.CustomerID,
		Feature:        p.Feature,
		Provider:       p.Provider,
		Model:          modelUsed,
		RequestedModel: p.Model,
		Usage:          call.Usage,
		CostUSD:        call.CostUSD,
		Outcome:        outcome,
		DecisionID:     decision.ID,
	})
	if decision.ID != "" {
		ack := AckProceededAsRequested
		if decision.Action == ActionDowngrade {
			ack = AckUsedDowngradeModel
		}
		c.Acknowledge(decision.ID, ack)
	}
	return GuardOutcome{Kind: GuardCompleted, Decision: decision}, nil
}

// Flush waits for queued events and acknowledgments. It never panics, and it
// returns when ctx is done even if work is still in flight.
func (c *Client) Flush(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// Close flushes and stops accepting background work.
func (c *Client) Close() {
	c.once.Do(func() { close(c.closed) })
	c.wg.Wait()
}

// ---------------------------------------------------------------- internals

func (c *Client) background(fn func(context.Context)) {
	select {
	case <-c.closed:
		// Losing an event is bad. Panicking in the caller's goroutine is worse.
		return
	default:
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		fn(ctx)
	}()
}

func (c *Client) report(err error, context string) {
	if c.onError == nil || err == nil {
		return
	}
	defer func() { _ = recover() }() // a broken hook is not our failure mode
	c.onError(err, context)
}

func (c *Client) post(ctx context.Context, path string, body any) (int, []byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("authorization", "Bearer "+c.apiKey)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("user-agent", userAgent)

	res, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = res.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return res.StatusCode, nil, err
	}
	return res.StatusCode, raw, nil
}

func newEventID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// The id is an idempotency key, not a secret. A clock-derived value is
		// worse but still unique enough, and far better than failing a call.
		return "evt_" + fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return "evt_" + h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func snippet(raw []byte) string {
	if len(raw) > 200 {
		return string(raw[:200])
	}
	return string(raw)
}
