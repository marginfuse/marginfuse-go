# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.0]

### Added

- `Identify`: tell MarginFuse who a customer is and which plan they are on.

  MarginFuse can now compute margin without a revenue source connected, from
  plans you declare in Settings and a plan assigned per customer. This call is
  how your application assigns that plan itself.

  ```go
  id, err := mf.Identify(ctx, marginfuse.IdentifyParams{
      CustomerID: "user_8x2m91",
      Plan:       "pro",
  })
  ```

  `Plan` is the key of a plan declared in MarginFuse, not a Stripe price id.
  Safe to call on every sign-in: sending the plan the customer is already on
  changes nothing. `PeriodStart` backdates the cycle, `ClearPlan` ends it.

  It is the one method that returns an error, and the only one that should.
  `Decide` fails open and `Track` retries, because both have a safe default;
  "I could not record what this customer pays" has none, and a wrong plan is a
  wrong margin. The error also reaches `Config.OnError`.

- `Plan` on `DecideParams` and `TrackParams`, so a plan can ride along with
  usage rather than needing its own call. There it is a hint: a key that does
  not resolve is ignored rather than failing your event, because usage must
  never be lost to a plan note.

Both are additive. Existing code keeps compiling and behaving unchanged.

## [0.1.0]

First release. Go 1.21+, zero dependencies, standard library only.

### Added

- `Client.Track` reports an AI call that already happened. Returns immediately,
  sends in the background with retries, and never panics into application code.
- `Client.Decide` asks whether the next call should run. Fails open to
  `ActionAllow` with `Degraded` set on any timeout or error.
- `Client.Guard` does the whole loop: ask, run your callback with the resolved
  model, report the real cost, acknowledge what the application did.
- `Client.Flush` and `Client.Close`, for workers and jobs that would otherwise
  exit before their last events are sent.
- `FromOpenRouter` maps an OpenRouter usage object, including the gateway's own
  cost, so gateway figures are exact rather than estimated.

### Notes on the design

- **`Decide` returns no error.** A failed decision is not a condition to branch
  on: it is an allow with `Degraded` set, because MarginFuse being unreachable
  must never become your outage. Transport failures reach `Config.OnError`.
- **`Guard` takes a callback.** If it returned a decision to act on, forgetting
  the check once would let a blocked request reach the provider. With a callback
  that cannot happen.
- **A zero in `Usage` means not reported.** The field is omitted from the
  request rather than sent as zero, because those are different claims.
- Verified against
  [marginfuse/sdk-contract](https://github.com/marginfuse/sdk-contract): 16
  behavioral scenarios and 13 gateway vectors, the same ones the Node and Python
  SDKs pass.
