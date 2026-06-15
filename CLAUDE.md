# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`starpkg/email` is an **L4 domain module** of the Star\* ecosystem: it lets Starlark scripts send email. A script `load`s the module and calls `send(...)`; the module assembles a request and ships it through the [Resend](https://resend.com) HTTP API, returning a result struct that describes the outcome.

The starpkg charter is **support for necessary local operations + simple abstractions over common online services, for ease of use.** `email` sits squarely on the **online-service** side — it is a thin wrapper over one SaaS (Resend). Its only local capability is `attachment_file`, which reads host files to attach them; everything else is network I/O. There is **no connection or session object**: `send` is a one-shot call, unlike `sqlite`'s connection surface.

Layer position: depends downward on `starpkg/base` (the config/module system), `1set/starlet` (the Machine, `dataconv`, and the `dataconv/types` arg helpers), transitively `1set/starlight` + `go.starlark.net`, plus the third-party `github.com/resend/resend-go/v2` SDK. Nothing in the ecosystem depends on it.

## Dev commands

Pure Go library with a Makefile. From this repo:

```bash
make test                                  # -race -cover, the working bar
make ci                                    # -race -cover profile + bench compile (what CI runs)
go test ./... -run TestComposeRequestValid # a single test
gofmt -l . && go vet ./...                 # must be clean before commit
go run github.com/1set/meta/doccov@master .  # doc-coverage gate (must exit 0)
```

**Verify on the go floor in Docker** — this repo's floor is **go 1.22** (see Release discipline), and the local toolchain is newer. Behavior on the floor must be checked in a container:

```bash
docker run --rm -v "$PWD":/src -v "$HOME/go/pkg/mod":/go/pkg/mod -w /src golang:1.22 go test -race -count=1 ./...
```

The live send test (`TestSendIntegration`) self-skips unless `EMAIL_RUN_INTEGRATION=1` **and** `EMAIL_RESEND_API_KEY` are set; never commit credentials. When run, it uses Resend's `onboarding@resend.dev` sender + `delivered@resend.dev` recipient, which need no domain setup. Integration scripts under `../test/email/*.star` live in the **private `starpkg/test` repo** and auto-skip when that directory is absent (e.g. in CI).

## Architecture (the part that spans files)

The module is small: one source file, one entry point. The shape is **config module + a single `send` builtin** that does parse → validate → compose → transport → marshal-result.

- **`resend.go`** — the whole module.
  - `Module` wraps a `base.ConfigurableModule` plus its `base.ConfigurableModuleExt` (`ext`, the typed config accessor). `NewModule()` declares two config options — `resend_api_key` (marked `SetSecret(true)`) and `sender_domain` — with no preset values; `NewModuleWithConfig(resendAPIKey, senderDomain)` is the same but seeds those values from Go. `genConfigOption` is the shared builder: it derives the env var name as `EMAIL_<NAME>` (upper-cased `ModuleName + "_" + name`).
  - `LoadModule()` hands `base`'s loader the one additional builtin, `send` (registered under the Starlark name `email.send`). `base` then auto-generates `set_resend_api_key`, `set_sender_domain`, and `get_sender_domain` — **no `get_resend_api_key`, because secret options get no getter** (see Invariants).
  - `genSendFunc()` returns the `send` builtin. Argument parsing uses starlet's `dataconv/types` helpers: `StringOrBytes` / `NullableStringOrBytes` for scalar text, `OneOrMany[starlark.String]` for the address lists (so `to`/`cc`/`bcc`/`attachment_file` accept a single string **or** a list), and `OneOrMany[*starlark.Dict]` for inline `attachment` dicts.
  - `sendArgs` + `composeRequest(args, senderDomain)` are the **pure, I/O-free** validation/assembly core (unit-testable without network): they enforce "one of html/text", "to non-empty", "one of sender/from_id", and build the `*resend.SendEmailRequest`. `resolveAddress(direct, nameID, domain, idField)` resolves a final address — a direct address wins; otherwise `nameID@domain` (erroring if the domain is unset).
  - Attachments are appended after compose: `attachment_file` paths are `os.ReadFile`'d from the host (filename = `filepath.Base`); inline `attachment` dicts must carry `name` + `content` keys.
  - Transport: `resend.NewClient(apiKey).Emails.SendWithContext(ctx, req)`, where `ctx` is pulled from the Starlark thread via `dataconv.GetThreadContext(thread)` (so host cancellation/timeout propagates).
  - Result marshalling builds a `starlarkstruct` (`starlarkstruct.FromStringDict`): on success it echoes the resolved request fields + the returned `id`; on a transport failure `success=False`, `error` holds the message, and the echoed fields are `None` — **except `attachments`, which is set from `req.Attachments` outside the success/failure branch** (so it survives a failed send when attachments were supplied). Earlier failures (missing key / validation / unreadable file) return a Starlark error, not a struct.

## Invariants / hardening (preserve when editing)

1. **Secret stays write-only.** `resend_api_key` is registered with `SetSecret(true)`; `base.LoadModule` deliberately emits **no `get_*` builtin** for secret options. A script can `set_resend_api_key(...)` but can never read the key back. Don't add a getter, and keep any new credential option secret.
2. **No host panics from script input.** The config-error-as-loader-error contract from `base` (PKG-03) means a missing/invalid key surfaces as a script error, not a host crash. `send` returns errors (missing API key, validation failures, unreadable attachment files, transport errors) as Starlark errors or as the result struct's `error` field — never a panic.
3. **Compose is pure.** `composeRequest`/`resolveAddress` perform no I/O so the validation rules stay unit-tested offline. Keep network and filesystem effects in the builtin body, not in these helpers.
4. **Host file access is a trusted capability.** `attachment_file` reads arbitrary host-readable paths. This is intentional but powerful — document it (README *Safety*) and don't widen it silently (e.g. no following of script-supplied URLs as if they were files).
5. **Backward compatibility (iron rule).** Old scripts must keep running. `NewModule()` and `NewModuleWithConfig` must keep their signatures; `send`'s keyword set is part of the contract. The `markdown=` keyword was **removed** (PKG-12) and a regression test (`TestSendRejectsMarkdownKeyword`) pins that it now errors — don't reintroduce in-module rendering; render Markdown upstream with the `markdown` module and pass `html=`.

## Test organization

Group by functional goal — **do not add one `*_test.go` per fix.** `email_test.go` is the single home, opened with a commented section list:

- *composeRequest validation / address resolution* — offline `composeRequest`/`resolveAddress` cases (table-driven).
- *send() through the Starlark module* — offline behavior (`TestSendRequiresAPIKey`, `TestSendRejectsMarkdownKeyword`).
- *live Resend integration* — opt-in `TestSendIntegration`, gated on `EMAIL_RUN_INTEGRATION` + a real key.

Add a new test as a **section here**, not a new file. Tests are table/example-driven; no third-party test framework. Keep functions small (Codacy's `nloc` rule).

## Documentation

Three layers must stay in sync (enforced by the doc standard, `plan/starpkg文档标准（DOC-STD）`):

- **`README.md`** — every script-facing symbol documented as a backtick whole-word: the `send` builtin (with its full keyword signature + result-struct fields) and the generated `set_resend_api_key` / `set_sender_domain` / `get_sender_domain`. Host levers (`attachment_file`, the secret key) under *Safety*. Names/signatures must match the code.
- **GoDoc** — package comment + a doc comment on every exported symbol (`ModuleName`, `Module`, `NewModule`, `NewModuleWithConfig`, `LoadModule`), first word = symbol name, gated by `revive`'s `exported` rule in CI.
- **doc-coverage gate** — `1set/meta`'s `doccov` enumerates the script-facing builtins and fails CI if any is missing a backtick mention in the README. Wired via `doc-coverage: true` in `.github/workflows/build.yml`.

## Release discipline

- **Floor = go 1.22**, declared in `go.mod` and pinned as the CI `go-floor`. A repo's floor only rises in its own dedicated pin PR.
- **CI matrix** = `[floor, latest stable]` via the centralized reusable workflow `1set/meta/.github/workflows/go-ci.yml` (pinned to a full commit SHA; bump the pin when meta's workflow changes).
- **Pinned deps** — `go.starlark.net` follows the ecosystem baseline (`ffb3f39…`); `1set/starlet`, `1set/starlight`, `starpkg/base` track their tagged releases. The `resend-go/v2` SDK is the one third-party transport dependency.
- **Bumping the version, the go floor, or tagging are user-confirmed actions** — never tag autonomously; default to patch bumps; published tags are immutable in the Go module proxy.
