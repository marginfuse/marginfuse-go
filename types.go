package marginfuse

import "time"

// ContractVersion is the version of the shared SDK contract this build was
// verified against.
//
// Module versions differ per language, because each tracks its own breaking
// changes: a rename in Python must not tell Go users something broke. What
// makes the SDKs interchangeable is this, not the module version. Two SDKs
// reporting the same contract version have passed the same scenarios and the
// same vectors.
//
// See github.com/marginfuse/sdk-contract.
const ContractVersion = 2

// Outcome is what happened to a provider call.
type Outcome string

const (
	OutcomeSuccess       Outcome = "success"
	OutcomeProviderError Outcome = "provider_error"
	OutcomeAppCancelled  Outcome = "app_cancelled"
	OutcomeTimeout       Outcome = "timeout"
)

// Action is a verdict. Enforce on this alone.
type Action string

const (
	ActionAllow         Action = "allow"
	ActionDowngrade     Action = "downgrade"
	ActionTopupRequired Action = "topup_required"
	ActionBlock         Action = "block"
)

// Acknowledgment is what the application actually did with a decision.
type Acknowledgment string

const (
	AckProceededAsRequested      Acknowledgment = "proceeded_as_requested"
	AckUsedDowngradeModel        Acknowledgment = "used_downgrade_model"
	AckPresentedTopup            Acknowledgment = "presented_topup"
	AckBlockedBeforeProviderCall Acknowledgment = "blocked_before_provider_call"
	AckFailedToApply             Acknowledgment = "failed_to_apply"
)

// Usage is what a provider call consumed.
//
// Zero means not reported, not "used none": the field is omitted from the
// request entirely, because claiming a call used zero input tokens is a
// different statement from not knowing what it used. Report what you have.
type Usage struct {
	InputTokens         int     `json:"inputTokens,omitempty"`
	OutputTokens        int     `json:"outputTokens,omitempty"`
	CachedInputTokens   int     `json:"cachedInputTokens,omitempty"`
	CacheCreationTokens int     `json:"cacheCreationTokens,omitempty"`
	Images              int     `json:"images,omitempty"`
	AudioSeconds        float64 `json:"audioSeconds,omitempty"`
}

// Decision is a verdict from MarginFuse.
//
// Degraded is true when MarginFuse could not reach a verdict and the request
// was allowed through unprotected. ID is empty in that case, which is exactly
// why enforcement must depend on Action alone.
type Decision struct {
	ID             string `json:"id,omitempty"`
	Action         Action `json:"action"`
	Model          string `json:"model"`
	Provider       string `json:"provider"`
	TopupContext   string `json:"topupContext,omitempty"`
	Degraded       bool   `json:"degraded"`
	DegradedReason string `json:"degradedReason,omitempty"`
}

// DecideParams asks about the call you are about to make.
//
// Plan is the key of a plan you declared in MarginFuse. It is a hint: a key
// that does not resolve is ignored rather than failing the decision.
type DecideParams struct {
	CustomerID    string
	Provider      string
	Model         string
	Plan          string
	Feature       string
	ExpectedUsage Usage
}

// TrackParams reports a call that already happened.
//
// EventID is the idempotency key. Leave it empty and one is generated; set it
// yourself when you already have an id you can retry with safely.
type TrackParams struct {
	EventID         string
	CustomerID      string
	Provider        string
	Model           string
	Plan            string
	Feature         string
	RequestedModel  string
	Usage           Usage
	CostUSD         string
	OccurredAt      time.Time
	Outcome         Outcome
	DecisionID      string
	RetryOfEventID  string
	CorrectsEventID string
}

// ProviderCall is what your callback did, handed back to Guard so it can be
// reported.
//
// CostUSD is a decimal string, not a float: money that round-trips through a
// float stops being the number the provider charged.
type ProviderCall struct {
	Usage   Usage
	CostUSD string
	Outcome Outcome
}

// IdentifyParams says who a customer is and what plan they pay for.
//
// Plan is the key of a plan you declared in MarginFuse Settings, not a Stripe
// price id. Leave it empty to change nothing about the plan; set ClearPlan to
// take the customer off plans entirely. PeriodStart backdates the cycle for a
// customer who has been paying since an earlier date.
type IdentifyParams struct {
	CustomerID  string
	Plan        string
	ClearPlan   bool
	PeriodStart time.Time
	Name        string
	Email       string
	Metadata    map[string]string
}

// Identity is what MarginFuse recorded for a customer.
//
// Plan is empty when the customer is on none.
type Identity struct {
	CustomerID  string `json:"customerId"`
	Plan        string `json:"plan"`
	PeriodStart string `json:"periodStart,omitempty"`
	PeriodEnd   string `json:"periodEnd,omitempty"`
}

// GuardKind is what Guard did.
type GuardKind string

const (
	GuardCompleted     GuardKind = "completed"
	GuardBlocked       GuardKind = "blocked"
	GuardTopupRequired GuardKind = "topup_required"
)

// GuardOutcome is the result of the whole loop.
type GuardOutcome struct {
	Kind     GuardKind
	Decision Decision
}
