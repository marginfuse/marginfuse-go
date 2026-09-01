# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
