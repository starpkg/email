# `email` — Starlark API reference

Full reference for the `email` module's script-facing surface. For an overview,
installation, and a quickstart, see the [README](../README.md).

A script loads the module and calls its builtins:

```python
load("email", "send", "set_resend_api_key", "set_sender_domain", "get_sender_domain")
```

The module exposes one functional builtin, `send`, plus the configuration
accessors generated from its config options (`set_resend_api_key`,
`set_sender_domain`, `get_sender_domain`). The accessors are documented in the
[Configuration](#configuration) section.

## Contents

- [`send`](#send)
  - [Parameters](#parameters)
  - [Result struct](#result-struct)
  - [Errors](#errors)
  - [Examples](#examples)
- [Configuration](#configuration)
- [Safety](#safety)

## `send`

```python
send(
    subject,
    html = ...,
    text = ...,
    to,
    cc = ...,
    bcc = ...,
    sender = ...,
    from_id = ...,
    reply_to = ...,
    reply_id = ...,
    attachment_file = ...,
    attachment = ...,
) -> struct
```

Assemble an email and ship it through the Resend API, returning a result struct
that describes the outcome. There is no connection or session object: `send` is
a one-shot call.

A sender address is resolved one of two ways: pass a full address as `sender`,
or pass a local-part as `from_id` and let the module form `from_id@<sender_domain>`
using the configured `sender_domain`. The same applies to the reply address via
`reply_to` (full) or `reply_id` (local-part). A direct address always wins over
the local-part form.

### Parameters

| Parameter | Type | Required | Description |
| --- | --- | --- | --- |
| `subject` | string | Yes | Email subject line. |
| `html` | string | No\* | HTML body content. |
| `text` | string | No\* | Plain-text body content. |
| `to` | string or list of strings | Yes | Recipient email address(es). |
| `cc` | string or list of strings | No | CC recipient email address(es). |
| `bcc` | string or list of strings | No | BCC recipient email address(es). |
| `sender` | string | No\*\* | Full sender email address. |
| `from_id` | string | No\*\* | Sender local-part; becomes `from_id@<sender_domain>`. Requires `sender_domain` to be set. |
| `reply_to` | string | No | Full reply-to email address. |
| `reply_id` | string | No | Reply-to local-part; becomes `reply_id@<sender_domain>`. Requires `sender_domain` to be set. |
| `attachment_file` | string or list of strings | No | Host file path(s) to read and attach (see [Safety](#safety)). |
| `attachment` | dict or list of dicts | No | Inline attachment(s), each a `{"name": string, "content": string}` object. |

\* At least one of `html` or `text` must be non-blank.

\*\* At least one of `sender` or `from_id` must be non-blank.

The list-valued parameters (`to`, `cc`, `bcc`, `attachment_file`, `attachment`)
accept either a single value or a list of values.

### Result struct

`send` returns a struct with the following fields:

| Field | Type | Description |
| --- | --- | --- |
| `success` | bool | Whether the email was sent successfully. |
| `error` | string or None | Error message if sending failed, else `None`. |
| `id` | string or None | Unique identifier of the sent email. |
| `sender` | string or None | The resolved sender (`from`) address. |
| `to` | list of strings or None | Recipient addresses. |
| `cc` | list of strings or None | CC recipient addresses. |
| `bcc` | list of strings or None | BCC recipient addresses. |
| `reply_to` | string or None | The resolved reply-to address. |
| `subject` | string or None | The email subject. |
| `body_html` | string or None | The HTML body. |
| `body_text` | string or None | The plain-text body. |
| `attachments` | list of dicts or None | Attachment details; each dict has `name` (string) and `content` (bytes). |

On a transport failure (the Resend API call returning an error), `success` is
`False`, `error` holds the message, and every echoed field is `None` — except
`attachments`, which still reflects any attachments that were supplied (and is
`None` when none were). On success, `error` is `None` and the echoed request
fields plus the returned `id` are populated.

### Errors

Failures fall into two kinds:

- **Transport failures** — the Resend API call returns an error. These are
  reported through the result struct (`success = False`, `error` set), not as a
  script error, so a script can branch on `result.success`.
- **Earlier failures** — these are raised as a Starlark error (they stop the
  script unless caught), and never reach Resend:
  - the Resend API key is not set (`resend_api_key is not set`);
  - validation fails: neither `html` nor `text` is provided, `to` is empty, or
    neither `sender` nor `from_id` is provided;
  - `from_id` or `reply_id` is used while `sender_domain` is unset;
  - an `attachment_file` path cannot be read from the host;
  - an inline `attachment` dict is missing its `name` or `content` key.

### Examples

Minimal send:

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

Multiple recipients and host-file attachments:

```python
load("email", "send")

result = send(
    subject = "Quarterly Report",
    text = "Please find the Q3 report attached.",
    to = ["manager@example.com", "director@example.com"],
    cc = "team@example.com",
    bcc = ["records@example.com", "audit@example.com"],
    sender = "reports@example.com",
    reply_to = "finance@example.com",
    attachment_file = ["reports/q3.pdf", "reports/summary.xlsx"],
)
```

Dynamic (inline) attachments built in the script, with `from_id` resolved
against the configured `sender_domain`:

```python
load("email", "send")

result = send(
    subject = "Generated Report",
    html = "<p>Your custom report is attached.</p>",
    to = "client@example.com",
    from_id = "reports",  # becomes reports@<sender_domain>
    attachment = [
        {"name": "report.csv", "content": "a,b,c\n1,2,3\n"},
        {"name": "note.txt", "content": "This is a text attachment"},
    ],
)
```

Rendering Markdown to HTML: this module only assembles and transports email; it
does not render Markdown. Render it with the `markdown` module first and pass the
result as `html`:

```python
load("email", "send")
load("markdown", "to_html")

result = send(
    subject = "Meeting Notes",
    html = to_html("# Team Meeting Notes\n\n- Update timeline\n- Schedule follow-up"),
    to = "team@example.com",
    from_id = "meetings",  # becomes meetings@<sender_domain>
)
```

## Configuration

The module's configuration is generated from two config options by the
underlying configurable module (`starpkg/base`). Each option gets a `set_<key>`
builtin to set it from Starlark and reads from an environment variable at module
construction; non-secret options also get a `get_<key>` builtin to read the
current value. A **secret** option gets **only** a `set_<key>` builtin — there is
no getter, so a script can never read the value back.

| Option | Accessors | Env var | Default | Description |
| --- | --- | --- | --- | --- |
| `resend_api_key` | `set_resend_api_key` (secret; **no getter**) | `EMAIL_RESEND_API_KEY` | `""` | Resend API key. Required before `send` can transport an email; sending errors with `resend_api_key is not set` until it is provided. Marked secret, so it has no `get_resend_api_key`. |
| `sender_domain` | `set_sender_domain` / `get_sender_domain` | `EMAIL_SENDER_DOMAIN` | `""` | Domain used to form an address from `from_id` / `reply_id` (`<local-part>@<sender_domain>`). Optional; only required when `from_id` or `reply_id` is used. |

Accessor signatures:

| Builtin | Signature | Description |
| --- | --- | --- |
| `set_resend_api_key` | `set_resend_api_key(value)` | Set the Resend API key (secret; no getter is exposed). |
| `set_sender_domain` | `set_sender_domain(value)` | Set the sender domain used with `from_id` / `reply_id`. |
| `get_sender_domain` | `get_sender_domain() -> str` | Read the configured sender domain. |

The values can also be seeded from Go (`NewModuleWithConfig`) or left to the
environment variables / the `set_*` builtins (`NewModule`) — see the
[README](../README.md#installation).

## Safety

`attachment_file` reads arbitrary paths on the **host** filesystem and attaches
their contents — a host-trusted capability. Only enable this module for scripts
you trust with host file access; an untrusted script could exfiltrate any
host-readable file by attaching it and mailing it out. Inline `attachment`
content carries no such host access — it is supplied entirely by the script.

The Resend API key is configured as a secret: it is write-only from Starlark
(`set_resend_api_key`, no `get_resend_api_key`), so a script cannot read the key
back.
