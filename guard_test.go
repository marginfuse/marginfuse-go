package marginfuse_test

// What Guard reports has to describe the call that actually ran. A downgrade
// can cross vendors, so the two ways of getting that wrong - billing the
// vendor that was asked for rather than the one that answered, and
// acknowledging a downgrade that failed as though it never happened - are both
// invisible from inside the application and wrong in the margin afterwards.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	marginfuse "github.com/marginfuse/marginfuse-go"
)

// recorder is a MarginFuse stand-in: it answers with the verdict the test
// chose and keeps the events and acknowledgments that come back.
type recorder struct {
	server *httptest.Server

	mu     sync.Mutex
	events []map[string]any
	acks   []string
}

func newRecorder(t *testing.T, decision map[string]any) *recorder {
	t.Helper()
	r := &recorder{}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.URL.Path == "/v1/decisions":
			_ = json.NewEncoder(w).Encode(decision)

		case req.URL.Path == "/v1/events":
			var body struct {
				Events []map[string]any `json:"events"`
			}
			_ = json.NewDecoder(req.Body).Decode(&body)
			r.mu.Lock()
			r.events = append(r.events, body.Events...)
			r.mu.Unlock()
			w.WriteHeader(http.StatusAccepted)

		case strings.HasSuffix(req.URL.Path, "/ack"):
			var body struct {
				Acknowledgment string `json:"acknowledgment"`
			}
			_ = json.NewDecoder(req.Body).Decode(&body)
			r.mu.Lock()
			r.acks = append(r.acks, body.Acknowledgment)
			r.mu.Unlock()
			w.WriteHeader(http.StatusAccepted)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(r.server.Close)
	return r
}

// guard runs one Guard call against the stand-in and returns once the
// background work has drained, since that is the only point at which what the
// SDK sent is a fact rather than a race.
func (r *recorder) guard(
	t *testing.T,
	p marginfuse.DecideParams,
	run func(context.Context, marginfuse.Decision) (marginfuse.ProviderCall, error),
) (marginfuse.GuardOutcome, error) {
	t.Helper()
	mf, err := marginfuse.New(marginfuse.Config{
		APIKey:     "sk_test",
		BaseURL:    r.server.URL,
		HTTPClient: r.server.Client(),
		OnError: func(err error, context string) {
			t.Errorf("unexpected %s error: %v", context, err)
		},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	ctx := context.Background()
	outcome, runErr := mf.Guard(ctx, p, run)

	flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	mf.Flush(flushCtx)
	return outcome, runErr
}

func (r *recorder) onlyEvent(t *testing.T) map[string]any {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) != 1 {
		t.Fatalf("got %d events, want exactly 1", len(r.events))
	}
	return r.events[0]
}

func (r *recorder) onlyAck(t *testing.T) string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.acks) != 1 {
		t.Fatalf("got %d acknowledgments, want exactly 1", len(r.acks))
	}
	return r.acks[0]
}

func succeeds(_ context.Context, _ marginfuse.Decision) (marginfuse.ProviderCall, error) {
	return marginfuse.ProviderCall{Usage: marginfuse.Usage{InputTokens: 120, OutputTokens: 30}}, nil
}

// The server can send a request to another vendor entirely. Reporting the
// vendor that was asked for prices the call from the wrong catalog, credits
// the wrong vendor, and computes the saving the downgrade exists to prove
// against a basis nothing ran on.
func TestGuardReportsTheVendorThatActuallyRan(t *testing.T) {
	r := newRecorder(t, map[string]any{
		"id":       "dec_cross",
		"action":   "downgrade",
		"model":    "claude-haiku-4-5",
		"provider": "anthropic",
	})

	if _, err := r.guard(t, marginfuse.DecideParams{
		CustomerID: "cus_1",
		Provider:   "openai",
		Model:      "gpt-4o",
	}, succeeds); err != nil {
		t.Fatalf("guard: %v", err)
	}

	event := r.onlyEvent(t)
	if event["provider"] != "anthropic" {
		t.Errorf("provider: got %v, want anthropic", event["provider"])
	}
	if event["model"] != "claude-haiku-4-5" {
		t.Errorf("model: got %v, want claude-haiku-4-5", event["model"])
	}
	// The request the customer made is still on the event, which is what makes
	// the saving computable at all.
	if event["requestedModel"] != "gpt-4o" {
		t.Errorf("requestedModel: got %v, want gpt-4o", event["requestedModel"])
	}
}

// Nothing was downgraded, so nothing moves. The decision defaults its provider
// to the requested one when the server sends none, and the event has to say
// the same thing it always did.
func TestGuardReportsTheRequestedVendorWhenNothingWasDowngraded(t *testing.T) {
	r := newRecorder(t, map[string]any{"id": "dec_allow", "action": "allow"})

	if _, err := r.guard(t, marginfuse.DecideParams{
		CustomerID: "cus_1",
		Provider:   "openai",
		Model:      "gpt-4o",
	}, succeeds); err != nil {
		t.Fatalf("guard: %v", err)
	}

	event := r.onlyEvent(t)
	if event["provider"] != "openai" {
		t.Errorf("provider: got %v, want openai", event["provider"])
	}
	if event["model"] != "gpt-4o" {
		t.Errorf("model: got %v, want gpt-4o", event["model"])
	}
	if ack := r.onlyAck(t); ack != string(marginfuse.AckProceededAsRequested) {
		t.Errorf("acknowledgment: got %s, want %s", ack, marginfuse.AckProceededAsRequested)
	}
}

// The cheaper model is the one that was called, whether or not the vendor then
// answered. Acknowledging "proceeded as requested" here would report the
// policy as unenforced every time a provider happened to fail.
func TestGuardAcknowledgesADowngradeWhoseCallFailed(t *testing.T) {
	r := newRecorder(t, map[string]any{
		"id":       "dec_cross",
		"action":   "downgrade",
		"model":    "claude-haiku-4-5",
		"provider": "anthropic",
	})

	boom := errors.New("provider exploded")
	outcome, err := r.guard(t, marginfuse.DecideParams{
		CustomerID: "cus_1",
		Provider:   "openai",
		Model:      "gpt-4o",
	}, func(context.Context, marginfuse.Decision) (marginfuse.ProviderCall, error) {
		return marginfuse.ProviderCall{}, boom
	})

	// The application's own error still propagates untouched.
	if !errors.Is(err, boom) {
		t.Fatalf("guard returned %v, want the provider's own error", err)
	}
	if outcome.Kind != marginfuse.GuardCompleted {
		t.Errorf("kind: got %s, want %s", outcome.Kind, marginfuse.GuardCompleted)
	}

	if ack := r.onlyAck(t); ack != string(marginfuse.AckUsedDowngradeModel) {
		t.Errorf("acknowledgment: got %s, want %s", ack, marginfuse.AckUsedDowngradeModel)
	}

	// The attempt is recorded because the vendor may still have charged for
	// it, and it is the downgrade's vendor that would have.
	event := r.onlyEvent(t)
	if event["provider"] != "anthropic" {
		t.Errorf("provider: got %v, want anthropic", event["provider"])
	}
	if event["model"] != "claude-haiku-4-5" {
		t.Errorf("model: got %v, want claude-haiku-4-5", event["model"])
	}
	if event["outcome"] != string(marginfuse.OutcomeProviderError) {
		t.Errorf("outcome: got %v, want %s", event["outcome"], marginfuse.OutcomeProviderError)
	}
}
