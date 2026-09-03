// Command conformance-runner drives this SDK through one shared conformance
// scenario.
//
// It reads a scenario as JSON on stdin, runs it against the mock server the
// driver started, and prints one JSON report on stdout. See
// contract/harness/runners/README.md for the contract.
//
// It exits non-zero only if the runner itself broke. An SDK misbehaving is a
// report for the driver to judge, not a crash here.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	marginfuse "github.com/marginfuse/marginfuse-go"
)

type scenario struct {
	Action  string `json:"action"`
	Options struct {
		TimeoutMS int `json:"timeoutMs"`
	} `json:"options"`
	Params   params `json:"params"`
	Provider struct {
		Throws bool             `json:"throws"`
		Usage  marginfuse.Usage `json:"usage"`
	} `json:"provider"`
}

// The scenarios speak the wire's camelCase.
type params struct {
	CustomerID     string            `json:"customerId"`
	Plan           string            `json:"plan"`
	ClearPlan      bool              `json:"clearPlan"`
	PeriodStart    string            `json:"periodStart"`
	Name           string            `json:"name"`
	Email          string            `json:"email"`
	Metadata       map[string]string `json:"metadata"`
	Feature        string            `json:"feature"`
	Provider       string            `json:"provider"`
	Model          string            `json:"model"`
	RequestedModel string            `json:"requestedModel"`
	EventID        string            `json:"eventId"`
	CostUSD        string            `json:"costUsd"`
	DecisionID     string            `json:"decisionId"`
	Acknowledgment string            `json:"acknowledgment"`
	Outcome        string            `json:"outcome"`
	Usage          marginfuse.Usage  `json:"usage"`
	ExpectedUsage  marginfuse.Usage  `json:"expectedUsage"`
}

type report struct {
	Outcome         string           `json:"outcome"`
	Threw           string           `json:"threw,omitempty"`
	Result          any              `json:"result,omitempty"`
	ProviderCalls   []map[string]any `json:"providerCalls"`
	OnErrorContexts []string         `json:"onErrorContexts"`
}

func main() {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		fail(err)
	}
	var s scenario
	if err := json.Unmarshal(raw, &s); err != nil {
		fail(err)
	}

	providerCalls := []map[string]any{}
	onErrorContexts := []string{}

	cfg := marginfuse.Config{
		APIKey:  os.Getenv("MARGINFUSE_API_KEY"),
		BaseURL: os.Getenv("MARGINFUSE_BASE_URL"),
		OnError: func(_ error, context string) {
			onErrorContexts = append(onErrorContexts, context)
		},
	}
	if s.Options.TimeoutMS > 0 {
		cfg.Timeout = time.Duration(s.Options.TimeoutMS) * time.Millisecond
	}
	mf, err := marginfuse.New(cfg)
	if err != nil {
		fail(err)
	}

	ctx := context.Background()
	out := report{Outcome: "returned"}
	p := s.Params

	switch s.Action {
	case "decide":
		d := mf.Decide(ctx, marginfuse.DecideParams{
			CustomerID:    p.CustomerID,
			Plan:          p.Plan,
			Feature:       p.Feature,
			Provider:      p.Provider,
			Model:         p.Model,
			ExpectedUsage: p.ExpectedUsage,
		})
		out.Result = decisionJSON(d)

	case "track":
		mf.Track(marginfuse.TrackParams{
			EventID:        p.EventID,
			CustomerID:     p.CustomerID,
			Plan:           p.Plan,
			Feature:        p.Feature,
			Provider:       p.Provider,
			Model:          p.Model,
			RequestedModel: p.RequestedModel,
			Usage:          p.Usage,
			CostUSD:        p.CostUSD,
			Outcome:        marginfuse.Outcome(p.Outcome),
			DecisionID:     p.DecisionID,
		})

	case "acknowledge":
		mf.Acknowledge(p.DecisionID, marginfuse.Acknowledgment(p.Acknowledgment))

	case "identify":
		// The one call that returns an error instead of failing open: a wrong
		// plan is a wrong margin, so the application has to be able to see it.
		ip := marginfuse.IdentifyParams{
			CustomerID: p.CustomerID,
			Plan:       p.Plan,
			ClearPlan:  p.ClearPlan,
			Name:       p.Name,
			Email:      p.Email,
			Metadata:   p.Metadata,
		}
		if p.PeriodStart != "" {
			when, err := time.Parse(time.RFC3339, p.PeriodStart)
			if err != nil {
				fail(err)
			}
			ip.PeriodStart = when
		}
		id, idErr := mf.Identify(ctx, ip)
		result := map[string]any{"ok": idErr == nil}
		if idErr != nil {
			result["error"] = idErr.Error()
		} else {
			result["customerId"] = id.CustomerID
			result["plan"] = id.Plan
			result["periodStart"] = id.PeriodStart
			result["periodEnd"] = id.PeriodEnd
		}
		out.Result = result

	case "guard":
		outcome, runErr := mf.Guard(ctx,
			marginfuse.DecideParams{
				CustomerID:    p.CustomerID,
				Plan:          p.Plan,
				Feature:       p.Feature,
				Provider:      p.Provider,
				Model:         p.Model,
				ExpectedUsage: p.ExpectedUsage,
			},
			func(_ context.Context, d marginfuse.Decision) (marginfuse.ProviderCall, error) {
				providerCalls = append(providerCalls, map[string]any{
					"model":    d.Model,
					"provider": d.Provider,
				})
				if s.Provider.Throws {
					return marginfuse.ProviderCall{}, errors.New("provider exploded")
				}
				return marginfuse.ProviderCall{Usage: s.Provider.Usage}, nil
			})
		if runErr != nil {
			// Go returns the provider's error rather than panicking, which is
			// this language's form of "propagate": the driver's `throws`
			// expectation is about the caller seeing the failure, not about
			// the mechanism.
			out.Outcome = "threw"
			out.Threw = runErr.Error()
		} else {
			// Only the discriminant and the decision travel; the application's
			// own result means nothing to another language.
			out.Result = map[string]any{
				"kind":     string(outcome.Kind),
				"decision": decisionJSON(outcome.Decision),
			}
		}

	default:
		fail(fmt.Errorf("unknown action %q", s.Action))
	}

	// Always flush, including after an error: the driver asserts on what the
	// SDK sent, and Guard records the attempt before it returns.
	flushCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	mf.Flush(flushCtx)

	out.ProviderCalls = providerCalls
	out.OnErrorContexts = onErrorContexts

	encoded, err := json.Marshal(out)
	if err != nil {
		fail(err)
	}
	fmt.Println(string(encoded))
}

func decisionJSON(d marginfuse.Decision) map[string]any {
	return map[string]any{
		"id":             d.ID,
		"action":         string(d.Action),
		"model":          d.Model,
		"provider":       d.Provider,
		"topupContext":   d.TopupContext,
		"degraded":       d.Degraded,
		"degradedReason": d.DegradedReason,
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
