# 📧 `email` — Email for Starlark via Resend

[![godoc](https://pkg.go.dev/badge/github.com/starpkg/email.svg)](https://pkg.go.dev/github.com/starpkg/email)
[![license](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Go Report Card](https://goreportcard.com/badge/github.com/starpkg/email)](https://goreportcard.com/report/github.com/starpkg/email)
[![codecov](https://codecov.io/gh/starpkg/email/graph/badge.svg)](https://codecov.io/gh/starpkg/email)
![binary footprint](https://img.shields.io/badge/binary_footprint-%2B0.4_MB-blue)

Send email from Starlark through the [Resend](https://resend.com/) API, with
support for HTML and plain-text bodies, CC/BCC recipients, reply-to, and
attachments.

## Overview

`starpkg` modules give Starlark scripts **support for necessary local
operations plus simple abstractions over common online services, for ease of
use.** `email` is firmly on the **online-service** side: it is a thin wrapper
over the Resend email API. Its one local touch point is `attachment_file`,
which reads host files to attach them (see [Safety](docs/API.md#safety)).

A script `load`s the module, calls `send(...)`, and gets back a result struct
describing the outcome — there is no connection object or session to manage.

## Installation

```bash
go get github.com/starpkg/email
```

Wire the module into a [starlet](https://github.com/1set/starlet) machine. Use
`NewModuleWithConfig` to pass credentials from Go, or `NewModule` to leave them
unset and supply them at runtime via the `EMAIL_RESEND_API_KEY` /
`EMAIL_SENDER_DOMAIN` environment variables or the `set_*` builtins.

```go
package main

import (
    "github.com/1set/starlet"
    "github.com/starpkg/email"
)

func main() {
    // Pre-configured: pass the Resend API key and sender domain directly.
    emailModule := email.NewModuleWithConfig(
        "re_123456789", // Resend API key
        "example.com",  // sender domain for from_id / reply_id
    )
    // Or: emailModule := email.NewModule()  // reads EMAIL_RESEND_API_KEY / EMAIL_SENDER_DOMAIN

    machine := starlet.NewDefault()
    machine.SetScript("send.star", []byte(`
load("email", "send")

result = send(
    subject = "Hello from Starlark!",
    html = "<h1>Hello World</h1><p>This is a test email.</p>",
    to = "recipient@example.com",
    sender = "sender@example.com",
)
print("sent" if result.success else result.error)
`), nil)
    machine.SetLazyloadModules(starlet.ModuleLoaderMap{
        email.ModuleName: emailModule.LoadModule(),
    })
    if _, err := machine.Run(); err != nil {
        panic(err)
    }
}
```

## Quickstart

```python
load("email", "send")

result = send(
    subject = "Hello from Starlark!",
    html = "<h1>Hello World</h1><p>This is a test email.</p>",
    to = "recipient@example.com",
    sender = "sender@example.com",
)

if result.success:
    print("Email sent, id:", result.id)
else:
    print("Failed:", result.error)
```

`send` accepts a single string or a list for `to` / `cc` / `bcc`, attaches host
files via `attachment_file` or inline content via `attachment`, and resolves a
sender from either a full `sender` address or a `from_id` local-part combined
with the configured `sender_domain`:

```python
load("email", "send")

result = send(
    subject = "Quarterly Report",
    text = "Please find the Q3 report attached.",
    to = ["manager@example.com", "director@example.com"],
    cc = "team@example.com",
    from_id = "reports",  # becomes reports@<sender_domain>
    attachment_file = "reports/q3.pdf",
)
```

## Starlark API at a glance

After `load('email', ...)`:

- `send(subject, html=, text=, to, cc=, bcc=, sender=, from_id=, reply_to=, reply_id=, attachment_file=, attachment=) -> struct` — send an email and return a result struct.
- `set_resend_api_key(value)` — set the Resend API key (secret; no getter).
- `set_sender_domain(value)` — set the sender domain used with `from_id` / `reply_id`.
- `get_sender_domain() -> str` — read the configured sender domain.

See **[docs/API.md](docs/API.md)** for the full reference: the complete `send`
signature, parameters, result-struct fields, errors, and examples.

## Configuration

Two config options, `resend_api_key` (secret, set-only) and `sender_domain`, are
settable from Go, from the `EMAIL_RESEND_API_KEY` / `EMAIL_SENDER_DOMAIN`
environment variables, or from Starlark via the `set_*` builtins. See
[docs/API.md § Configuration](docs/API.md#configuration) for the full table.

## License

This project is licensed under the MIT License.
