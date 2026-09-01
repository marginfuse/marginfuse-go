package marginfuse

import (
	"math"
	"strconv"
	"strings"
)

// OpenRouterUsage is the shape this helper reads from an OpenRouter usage
// object. Structural on purpose: it accepts a decoded response from any
// client without either side importing the other's types.
type OpenRouterUsage struct {
	PromptTokens        float64                  `json:"prompt_tokens"`
	CompletionTokens    float64                  `json:"completion_tokens"`
	Cost                *float64                 `json:"cost"`
	PromptTokensDetails *OpenRouterPromptDetails `json:"prompt_tokens_details"`
}

// OpenRouterPromptDetails is the cache breakdown inside prompt_tokens.
type OpenRouterPromptDetails struct {
	CachedTokens     float64 `json:"cached_tokens"`
	CacheWriteTokens float64 `json:"cache_write_tokens"`
	AudioTokens      float64 `json:"audio_tokens"`
}

// FromOpenRouter maps an OpenRouter usage object to the MarginFuse fields.
//
//	var body struct{ Usage marginfuse.OpenRouterUsage `json:"usage"` }
//	json.Unmarshal(raw, &body)
//	usage, cost := marginfuse.FromOpenRouter(&body.Usage)
//	mf.Track(marginfuse.TrackParams{..., Usage: usage, CostUSD: cost})
//
// It exists because mapping the fields by hand gets two things silently
// wrong, and both misstate margin without producing an error anywhere:
//
// First, prompt_tokens is the TOTAL input count. Cached reads and cache writes
// are already inside it, and MarginFuse prices those as three separate charges
// and adds them up, so passing the total straight through charges every cached
// token twice at the full uncached rate.
//
// Second, cost is a float, and strconv.FormatFloat with 'g' renders small ones
// in exponent notation ("1.2e-07"), which the API rejects as a decimal string.
//
// The returned cost is empty when the response carried none, which lets the
// event fall through to MarginFuse's own pricing instead of claiming a $0
// charge.
func FromOpenRouter(u *OpenRouterUsage) (Usage, string) {
	if u == nil {
		return Usage{}, ""
	}

	var cached, cacheWrites int
	if u.PromptTokensDetails != nil {
		cached = toInt(u.PromptTokensDetails.CachedTokens)
		cacheWrites = toInt(u.PromptTokensDetails.CacheWriteTokens)
	}
	// What is left after the cached parts is what was billed at the full input
	// rate. Clamped at zero so a provider reporting these differently degrades
	// to "no fresh input" rather than a negative charge.
	fresh := toInt(u.PromptTokens) - cached - cacheWrites
	if fresh < 0 {
		fresh = 0
	}

	usage := Usage{
		InputTokens:         fresh,
		OutputTokens:        toInt(u.CompletionTokens),
		CachedInputTokens:   cached,
		CacheCreationTokens: cacheWrites,
	}

	if u.Cost == nil || math.IsNaN(*u.Cost) || math.IsInf(*u.Cost, 0) || *u.Cost < 0 {
		return usage, ""
	}
	return usage, creditsToUSD(*u.Cost)
}

func toInt(v float64) int {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return 0
	}
	return int(math.Round(v))
}

// creditsToUSD renders OpenRouter credits (1 credit = 1 USD) as a decimal
// string the API accepts.
//
// Fixed point to nano precision: 'g' formatting emits exponent notation for
// the small costs cheap models produce, and money below a nano cannot be
// represented at all, so it rounds down rather than pretending otherwise.
func creditsToUSD(cost float64) string {
	s := strconv.FormatFloat(cost, 'f', 9, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}
