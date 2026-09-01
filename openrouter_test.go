package marginfuse_test

// Driven entirely by contract/conformance/gateway-vectors.json, which every
// SDK in every language reads. Assertions written here instead would be a
// second copy of the truth, and this SDK would slowly stop agreeing with the
// others. To add a case, edit the vector file, not this test.

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"

	marginfuse "github.com/marginfuse/marginfuse-go"
)

type vectorFile struct {
	Adapters map[string]struct {
		Cases []vectorCase `json:"cases"`
	} `json:"adapters"`
}

type vectorCase struct {
	Name      string                      `json:"name"`
	Input     *marginfuse.OpenRouterUsage `json:"input"`
	OmitInput bool                        `json:"omitInput"`
	Expected  struct {
		Usage   map[string]float64 `json:"usage"`
		CostUSD *string            `json:"costUsd"`
	} `json:"expected"`
}

func loadCases(t *testing.T) []vectorCase {
	t.Helper()
	raw, err := os.ReadFile("contract/conformance/gateway-vectors.json")
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var file vectorFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	adapter, ok := file.Adapters["fromOpenRouter"]
	if !ok {
		t.Fatal("gateway-vectors.json has no fromOpenRouter adapter")
	}
	if len(adapter.Cases) == 0 {
		t.Fatal("fromOpenRouter has no cases")
	}
	return adapter.Cases
}

// usageMap renders only the fields the adapter actually set, in wire names, so
// an omitted field and a zero are the same thing here as they are on the wire.
func usageMap(u marginfuse.Usage) map[string]float64 {
	out := map[string]float64{}
	for name, value := range map[string]float64{
		"inputTokens":         float64(u.InputTokens),
		"outputTokens":        float64(u.OutputTokens),
		"cachedInputTokens":   float64(u.CachedInputTokens),
		"cacheCreationTokens": float64(u.CacheCreationTokens),
		"images":              float64(u.Images),
		"audioSeconds":        u.AudioSeconds,
	} {
		if value != 0 {
			out[name] = value
		}
	}
	return out
}

func call(c vectorCase) (marginfuse.Usage, string) {
	if c.OmitInput {
		return marginfuse.FromOpenRouter(nil)
	}
	return marginfuse.FromOpenRouter(c.Input)
}

func TestGatewayVectors(t *testing.T) {
	for _, c := range loadCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			usage, cost := call(c)

			got := usageMap(usage)
			if len(got) != len(c.Expected.Usage) {
				t.Fatalf("usage: got %v, want %v", got, c.Expected.Usage)
			}
			for field, want := range c.Expected.Usage {
				if got[field] != want {
					t.Errorf("usage.%s: got %v, want %v", field, got[field], want)
				}
			}

			if c.Expected.CostUSD == nil {
				// Absent must mean absent, not present-and-zero: omitting the
				// cost lets MarginFuse price the call, where "0" would claim
				// it was free.
				if cost != "" {
					t.Errorf("costUsd: got %q, want it omitted", cost)
				}
				return
			}
			if cost != *c.Expected.CostUSD {
				t.Errorf("costUsd: got %q, want %q", cost, *c.Expected.CostUSD)
			}
		})
	}
}

func TestNeverProducesACostTheAPIWouldReject(t *testing.T) {
	// The decimal-string pattern from the API's own schema. Exponent notation
	// is the failure this guards, and it is silent everywhere else.
	decimal := regexp.MustCompile(`^\d+(\.\d+)?$`)
	for _, c := range loadCases(t) {
		if _, cost := call(c); cost != "" && !decimal.MatchString(cost) {
			t.Errorf("%s: %q is not a decimal string", c.Name, cost)
		}
	}
}
