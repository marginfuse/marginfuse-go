# marginfuse-go

[![Go Reference](https://pkg.go.dev/badge/github.com/marginfuse/marginfuse-go.svg)](https://pkg.go.dev/github.com/marginfuse/marginfuse-go)
[![ci](https://github.com/marginfuse/marginfuse-go/actions/workflows/ci.yml/badge.svg)](https://github.com/marginfuse/marginfuse-go/actions/workflows/ci.yml)
[![license](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

Server-side SDK for [MarginFuse](https://marginfuse.com): profitability
guardrails for AI SaaS. Connect revenue to per-request AI cost, see gross margin
per customer, and stop loss-making requests before they run.

- **Metadata only, by construction.** The event shape has no field for prompts
  or responses, so they cannot be sent. Not a policy, an absence.
- **Never breaks your app.** It does not panic into your code, and it does not
  block your request on MarginFuse being up. If MarginFuse is unreachable, your
  requests proceed unchanged.
- **Zero dependencies.** Standard library only, Go 1.21+.

> **Server side only.** This SDK carries a secret API key. Never ship it in a
> binary a user can run and read.

## Install

```bash
go get github.com/marginfuse/marginfuse-go
```

## Track an AI call

Monitoring. One call after each AI request, metadata only.

```go
mf, err := marginfuse.New(marginfuse.Config{APIKey: os.Getenv("MARGINFUSE_KEY")})
if err != nil {
    return err
}
defer mf.Close() // flushes

mf.Track(marginfuse.TrackParams{
    CustomerID: "cus_8x2m91", // your Stripe customer id, or your own
    Feature:    "ai_chat",
    Provider:   "openai",
    Model:      "gpt-4.1",
    Usage:      marginfuse.Usage{InputTokens: 1204, OutputTokens: 388},
})
```

`Track` returns immediately and sends in the background with retries. In a
worker, a cron job or a Lambda handler, call `Flush` before the process exits,
or the last events go with it.

A zero in `Usage` means not reported, not "used none": the field is left off the
request entirely, because claiming a call used zero input tokens is a different
statement from not knowing what it used.

## Guard a call

Protection. Ask before the call runs, and act on the answer.

```go
out, err := mf.Guard(ctx,
    marginfuse.DecideParams{
        CustomerID: "cus_8x2m91",
        Feature:    "ai_chat",
        Provider:   "openai",
        Model:      "gpt-4.1",
    },
    func(ctx context.Context, d marginfuse.Decision) (marginfuse.ProviderCall, error) {
        // d.Model is the one to call: a downgrade verdict changes it.
        r, err := client.CreateChatCompletion(ctx, request(d.Model, messages))
        if err != nil {
            return marginfuse.ProviderCall{}, err
        }
        return marginfuse.ProviderCall{
            Usage: marginfuse.Usage{
                InputTokens:  r.Usage.PromptTokens,
                OutputTokens: r.Usage.CompletionTokens,
            },
        }, nil
    })

if err != nil {
    return err // your provider's error, unchanged
}
switch out.Kind {
case marginfuse.GuardCompleted:
    // the call ran
case marginfuse.GuardTopupRequired:
    showTopup(out.Decision.TopupContext)
case marginfuse.GuardBlocked:
    showLimitReached()
}
```

One call does the whole loop: ask, run with the resolved model, report the real
cost, acknowledge what your application did.

### Why a callback

Enforcement must not depend on you remembering to check anything. If `Guard`
returned a decision for you to act on, forgetting the check once would mean a
blocked request reaches the provider anyway. With a callback that is
structurally impossible: when the verdict is `block`, your function is never
called.

### Why `Decide` returns no error

There is no failure a caller should branch on. A decision that times out or
errors is an *allow* with `Degraded` set, because MarginFuse being unreachable
must never become your outage. Transport failures go to `Config.OnError`.

## OpenRouter and other gateways

Gateways report the real cost of every call. Forward it and your figures are
exact instead of estimated.

```go
var body struct {
    Usage marginfuse.OpenRouterUsage `json:"usage"`
}
json.Unmarshal(raw, &body)

usage, cost := marginfuse.FromOpenRouter(&body.Usage)

mf.Track(marginfuse.TrackParams{
    CustomerID: "cus_8x2m91",
    Feature:    "ai_chat",
    Provider:   "openrouter",
    Model:      "anthropic/claude-sonnet-4.5",
    Usage:      usage,
    CostUSD:    cost,
})
```

Use the helper rather than mapping the fields yourself. OpenRouter's
`prompt_tokens` already includes cached reads and cache writes, which MarginFuse
prices separately, so passing it through directly charges every cached token
twice at the full input rate. The helper also formats the cost as a decimal
string, because `strconv.FormatFloat` with `'g'` produces `"1.2e-07"` for small
costs and the API rejects that.

## Configuration

```go
mf, err := marginfuse.New(marginfuse.Config{
    APIKey:     os.Getenv("MARGINFUSE_KEY"),
    BaseURL:    "https://api.marginfuse.com", // your own deployment in dev
    Timeout:    1500 * time.Millisecond,      // Decide budget before failing open
    OnError:    func(err error, ctx string) { log.Printf("marginfuse %s: %v", ctx, err) },
    HTTPClient: myClient,
})
```

`OnError` is the only place transport failures surface. The SDK swallows them so
they cannot become your outage; without the hook they are silent.

## What it sends

Everything, and nothing else:

```
eventId  customerId  feature  provider  model  requestedModel
usage { inputTokens, outputTokens, cachedInputTokens,
        cacheCreationTokens, images, audioSeconds }
costUsd  occurredAt  outcome  decisionId  retryOfEventId  correctsEventId
```

There is no field for message content anywhere in the wire types. The
[conformance suite](https://github.com/marginfuse/sdk-contract) checks this
against the bytes that actually leave the process, on every scenario.

## Conformance

This SDK is verified against
[marginfuse/sdk-contract](https://github.com/marginfuse/sdk-contract), the same
contract every MarginFuse SDK in every language is held to. It is a submodule
here, so the pinned commit records exactly which contract a release passed.

```bash
git clone --recurse-submodules https://github.com/marginfuse/marginfuse-go
cd marginfuse-go
go test ./...                          # unit tests, plus the shared gateway vectors
npm --prefix contract/harness install
npm --prefix contract/harness run conformance go
```

## Links

- [MarginFuse](https://marginfuse.com), product and pricing
- [Documentation](https://marginfuse.com/docs)
- [API reference](https://api.marginfuse.com/openapi.json)
- [Security policy](SECURITY.md)
- [Contributing](CONTRIBUTING.md)

MIT, Pemira Labs.
