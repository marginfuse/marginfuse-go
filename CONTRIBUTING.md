# Contributing

## Getting set up

The conformance contract is a submodule, so clone with it:

```bash
git clone --recurse-submodules https://github.com/marginfuse/marginfuse-go
cd marginfuse-go
go test ./...
```

If you already cloned without it: `git submodule update --init --recursive`.

## Before you open a pull request

```bash
gofmt -l .
go vet ./...
go test -race ./...

npm --prefix contract/harness install
npm --prefix contract/harness run conformance go
```

## Three rules worth knowing before you change behavior

**This SDK never panics into application code.** It sits in the request path of
somebody else's product. A transport error goes to `Config.OnError` and the call
proceeds. The one error it returns is whatever your own callback gave `Guard`,
because your error handling owns provider failures.

**`Guard` keeps its callback.** Returning a decision for the caller to act on
reads better and would be wrong: enforcement would depend on remembering a
check, and forgetting once means a blocked request reaches the provider.

**Behavior is defined in the contract, not here.** The expectations live in
[marginfuse/sdk-contract](https://github.com/marginfuse/sdk-contract) as data,
and every MarginFuse SDK in every language reads the same files. If you are
changing what the SDK does rather than how it does it, the change starts with a
pull request there.

## Releases are permanent

The Go module proxy caches tags forever. There is no unpublish, no yank, and no
72-hour window: a bad `v0.1.1` is public for good and the only remedy is
`v0.1.2`. So conformance passes before a tag is pushed, never after.

## Style

`gofmt` decides formatting. Comments explain why, not what. No em dashes.
