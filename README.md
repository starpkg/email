# 📧 `email` - Starlark Email Module for Resend API

A lightweight Starlark module for sending emails through the Resend API. Seamlessly integrate email capabilities into your Starlark scripts with support for HTML, plain text, attachments, and comprehensive recipient management.

## Overview

The `email` module provides a simple way to send emails from Starlark with features like:

- **HTML and plain text body support**
- **File attachments**
- **CC/BCC recipients**
- **Reply-to configuration**
- **Sender domain management**
- **Comprehensive response handling**
- **Graceful error handling**

## Installation

```bash
go get github.com/starpkg/email
```

## Quick Start

```go
package main

import (
    "github.com/starpkg/email"
    "github.com/1set/starlet"
)

func main() {
    // Create email module with API key and sender domain
    emailModule := email.NewModuleWithConfig(
        "re_123456789", // Resend API key
        "example.com",  // Sender domain for from_id/reply_id
    )

    // Load the module
    loader := emailModule.LoadModule()

    // Run Starlark code with the module
    starlet.Run(`
        load("email", "send")

        # Send an email with HTML content
        result = send(
            subject = "Hello from Starlark!",
            html = "<h1>Hello World</h1><p>This is a test email.</p>",
            to = "recipient@example.com",
            sender = "sender@example.com"
        )

        if result.success:
            print("Email sent successfully!")
            print("Email ID:", result.id)
            print("To:", result.to)
        else:
            print("Failed to send email:", result.error)
    `, loader)
}
```

## Configuration

The email module requires a Resend API key and optionally a sender domain:

1. **`resend_api_key`**: Your Resend API key (required)
2. **`sender_domain`**: Domain used with `from_id` and `reply_id` (optional)

You can configure these values in several ways:

```go
// Method 1: Empty module (configure in Starlark)
module := email.NewModule()

// Method 2: Pre-configured module
module := email.NewModuleWithConfig(
    "re_123456789",  // Resend API key
    "example.com",   // Sender domain
)

// Method 3: With dynamic getters
module := email.NewModuleWithGetter(
    func() string { return getAPIKeyFromVault() },
    func() string { return "example.com" },
)
```

## Usage in Starlark

### Basic Email

```python
load("email", "send")

# Simple email with HTML body
result = send(
    subject = "Hello from Starlark!",
    html = "<h1>Welcome!</h1><p>Your account has been created.</p>",
    to = "user@example.com",
    sender = "noreply@example.com"
)

if result.success:
    print("Email sent with ID:", result.id)
else:
    print("Failed to send email:", result.error)
```

### Rendering Markdown to HTML

This module only assembles and transports email (MIME + send); it does not
render Markdown. To send Markdown as HTML, render it with the `markdown` module
first and pass the result as `html`:

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

### Multiple Recipients and Attachments

```python
load("email", "send")

# Email with CC, BCC and attachments
result = send(
    subject = "Quarterly Report",
    text = "Please find the Q3 report attached.",
    to = ["manager@example.com", "director@example.com"],
    cc = "team@example.com",
    bcc = ["records@example.com", "audit@example.com"],
    sender = "reports@example.com",
    reply_to = "finance@example.com",
    attachment_file = ["reports/q3_2023.pdf", "reports/summary.xlsx"]
)

if result.success:
    print("Email sent to:", result.to)
    print("CC:", result.cc)
    print("BCC:", result.bcc)
    print("Sender:", result.sender)
    print("Attachments:", result.attachments)
```

### Dynamic Attachments

```python
load("email", "send")

# Email with dynamically created attachments
result = send(
    subject = "Generated Report",
    html = "<p>Your custom report is attached.</p>",
    to = "client@example.com",
    from_id = "reports",
    attachment = [
        {"name": "report.csv", "content": generate_csv_content()},
        {"name": "chart.txt", "content": "This is a text attachment"}
    ]
)

if result.success:
    print("Email sent with attachments:", result.attachments)
```

## API Reference

### Function: `send`

Sends an email via Resend API.

#### Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `subject` | string | Yes | Email subject line |
| `html` | string | No* | HTML body content |
| `text` | string | No* | Plain text body content |
| `to` | string or list of strings | Yes | Recipient email address(es) |
| `cc` | string or list of strings | No | CC recipient email address(es) |
| `bcc` | string or list of strings | No | BCC recipient email address(es) |
| `sender` | string | No** | Full sender email address |
| `from_id` | string | No** | Sender ID (used with domain) |
| `reply_to` | string | No | Reply-to email address |
| `reply_id` | string | No | Reply-to ID (used with domain) |
| `attachment_file` | string or list of strings | No | File path(s) to attach |
| `attachment` | list of dicts | No | List of `{"name": string, "content": string}` objects |

*At least one of `html` or `text` must be provided.
**At least one of `sender` or `from_id` must be provided.

#### Returns

A struct containing the following fields:

| Field | Type | Description |
|-------|------|-------------|
| `success` | bool | Whether the email was sent successfully |
| `error` | string | Error message if the email failed to send |
| `id` | string | The unique identifier of the sent email |
| `sender` | string | The sender's email address |
| `to` | list of strings | List of recipient email addresses |
| `cc` | list of strings | List of CC recipient email addresses |
| `bcc` | list of strings | List of BCC recipient email addresses |
| `reply_to` | string | The reply-to email address |
| `subject` | string | The email subject |
| `body_html` | string | The HTML content of the email |
| `body_text` | string | The plain text content of the email |
| `attachments` | list of dicts | List of attachment details (name, content) |

When an error occurs:

- `success` will be `False`
- `error` will contain the error message
- All other fields will be `None`

## Environment Integration

For deployment environments, you can use environment variables:

```go
module, _ := base.NewConfigurableModuleWithOptions(
    base.WithConfigEnvVar("resend_api_key", "RESEND_API_KEY"),
    base.WithConfigEnvVar("sender_domain", "EMAIL_SENDER_DOMAIN"),
)
emailModule := &email.Module{CfgMod: module}
```

## License

This project is licensed under the MIT License.
