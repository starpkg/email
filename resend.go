// Package email provides a Starlark module that sends email using Resend API.
// The module provides a send() function that returns a struct containing details about the sent email including:
// - success: Whether the email was sent successfully
// - error: Error message if the email failed to send
// - id: The unique identifier of the sent email
// - from: The sender's email address
// - to: List of recipient email addresses
// - cc: List of CC recipient email addresses
// - bcc: List of BCC recipient email addresses
// - reply_to: The reply-to email address
// - subject: The email subject
// - body_html: The HTML content of the email
// - body_text: The plain text content of the email
// - attachments: List of attachment details (name, content)
// - tags: List of email tags
// - headers: Map of custom email headers
// - scheduled_at: Scheduled send time if specified
package email

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/1set/gut/ystring"
	"github.com/1set/starlet"
	"github.com/1set/starlet/dataconv"
	"github.com/1set/starlet/dataconv/types"
	"github.com/resend/resend-go/v2"
	"github.com/samber/lo"
	"github.com/starpkg/base"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	renderer "github.com/yuin/goldmark/renderer/html"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// ModuleName defines the expected name for this module when used in Starlark's load() function, e.g., load('email', 'send')
const ModuleName = "email"

// Configuration key constants
const (
	configKeyResendAPIKey = "resend_api_key"
	configKeySenderDomain = "sender_domain"
)

var (
	none  = starlark.None // none is a convenience variable for starlark.None
	empty string          // empty is a convenience variable for an empty string
)

// Module wraps the ConfigurableModule with specific functionality for sending emails.
type Module struct {
	cfgMod *base.ConfigurableModule
	ext    *base.ConfigurableModuleExt
}

// NewModule creates a new instance of Module with default empty configurations.
func NewModule() *Module {
	return newModuleWithOptions(
		genConfigOption(configKeyResendAPIKey, "Resend API key", empty).SetSecret(true),
		genConfigOption(configKeySenderDomain, "Sender domain", empty),
	)
}

// NewModuleWithConfig creates a new instance of Module with the given configuration values.
func NewModuleWithConfig(resendAPIKey, senderDomain string) *Module {
	return newModuleWithOptions(
		genConfigOption(configKeyResendAPIKey, "Resend API key with preset value", resendAPIKey).SetSecret(true),
		genConfigOption(configKeySenderDomain, "Sender domain with preset value", senderDomain),
	)
}

// genConfigOption creates a configuration option with common settings.
// It sets up the name, description, default value, and environment variable, and marks it as secret if needed.
func genConfigOption(name, description, defaultValue string) *base.ConfigOption[string] {
	return base.NewConfigOption(defaultValue).
		WithName(name).
		WithDescription(description).
		WithEnvVar(strings.ToUpper(ModuleName + "_" + name))
}

// newModuleWithOptions creates a Module with the given configuration options.
func newModuleWithOptions(apiKeyOpt, senderDomainOpt *base.ConfigOption[string]) *Module {
	cm, _ := base.NewConfigurableModuleWithConfigOptions(
		apiKeyOpt,
		senderDomainOpt,
	)
	return &Module{
		cfgMod: cm,
		ext:    cm.Extend(),
	}
}

// LoadModule returns the Starlark module loader with the email-specific functions.
func (m *Module) LoadModule() starlet.ModuleLoader {
	// Additional module functions
	additionalFuncs := starlark.StringDict{
		"send": m.genSendFunc(),
	}
	return m.cfgMod.LoadModule(ModuleName, additionalFuncs)
}

// stringListToStarlark converts a slice of strings to a Starlark list of strings
func stringListToStarlark(strs []string) starlark.Value {
	return starlark.NewList(lo.Map(strs, func(s string, _ int) starlark.Value { return starlark.String(s) }))
}

// genSendFunc generates the Starlark callable function to send an email.
func (m *Module) genSendFunc() starlark.Callable {
	return starlark.NewBuiltin(ModuleName+".send", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		// Load config: resend_api_key is required, sender_domain is optional
		resendAPIKey := m.ext.GetString(configKeyResendAPIKey)
		if resendAPIKey == "" {
			return none, fmt.Errorf("%s is not set", configKeyResendAPIKey)
		}
		// Get sender domain, but don't require it yet - it's only needed for from_id/reply_id
		senderDomain := m.ext.GetString(configKeySenderDomain, "")

		// parse args
		newOneOrListStr := func() *types.OneOrMany[starlark.String] { return types.NewOneOrManyNoDefault[starlark.String]() }
		var (
			subject            types.StringOrBytes         // must be set
			bodyHTML           types.NullableStringOrBytes // one of the three must be set
			bodyText           types.NullableStringOrBytes
			bodyMarkdown       types.NullableStringOrBytes
			toAddresses        = newOneOrListStr() // must be set
			ccAddresses        = newOneOrListStr()
			bccAddresses       = newOneOrListStr()
			fromAddress        types.NullableStringOrBytes // one of the two must be set
			fromNameID         types.NullableStringOrBytes
			replyAddress       types.NullableStringOrBytes // both are optional
			replyNameID        types.NullableStringOrBytes
			attachmentFiles    = newOneOrListStr()
			attachmentContents = types.NewOneOrManyNoDefault[*starlark.Dict]()
		)
		if err := starlark.UnpackArgs(b.Name(), args, kwargs,
			"subject", &subject,
			"html?", &bodyHTML, "text?", &bodyText, "markdown?", &bodyMarkdown,
			"to", toAddresses, "cc?", ccAddresses, "bcc?", bccAddresses,
			"from?", &fromAddress, "from_id?", &fromNameID,
			"reply_to?", &replyAddress, "reply_id?", &replyNameID,
			"attachment_file?", attachmentFiles, "attachment?", attachmentContents); err != nil {
			return none, err
		}

		// validate args
		if body := []string{bodyHTML.GoString(), bodyText.GoString(), bodyMarkdown.GoString()}; lo.EveryBy(body, ystring.IsBlank) {
			return none, fmt.Errorf("one of body_html, body_text, or body_markdown must be non-blank")
		}
		if toAddresses.Len() == 0 {
			return none, fmt.Errorf("to must be set and non-empty")
		}
		if fromAddress.IsNullOrEmpty() && fromNameID.IsNullOrEmpty() {
			return none, fmt.Errorf("one of from or from_id must be non-blank")
		}

		// convert from to send address
		var sendAddr string
		if !fromAddress.IsNullOrEmpty() {
			sendAddr = fromAddress.GoString()
		} else if !fromNameID.IsNullOrEmpty() {
			if ystring.IsNotBlank(senderDomain) {
				sendAddr = fromNameID.GoString() + "@" + senderDomain
			} else {
				return none, fmt.Errorf("%s should be set when from_id is used", configKeySenderDomain)
			}
		} else {
			return none, fmt.Errorf("no valid from or from_id found")
		}

		// convert from to reply address
		var replyAddr string
		if !replyAddress.IsNullOrEmpty() {
			replyAddr = replyAddress.GoString()
		} else if !replyNameID.IsNullOrEmpty() {
			if ystring.IsNotBlank(senderDomain) {
				replyAddr = replyNameID.GoString() + "@" + senderDomain
			} else {
				return none, fmt.Errorf("%s should be set when reply_id is used", configKeySenderDomain)
			}
		}

		// prepare request
		convGoString := func(v []starlark.String) []string {
			l := make([]string, len(v))
			for i, vv := range v {
				l[i] = dataconv.StarString(vv)
			}
			return l
		}
		req := &resend.SendEmailRequest{
			From:    sendAddr,
			To:      convGoString(toAddresses.Slice()),
			Cc:      convGoString(ccAddresses.Slice()),
			Bcc:     convGoString(bccAddresses.Slice()),
			ReplyTo: replyAddr,
			Subject: subject.GoString(),
		}

		// for body content
		if !bodyHTML.IsNullOrEmpty() {
			// directly use HTML content
			req.Html = bodyHTML.GoString()
		}

		if !bodyText.IsNullOrEmpty() {
			// directly use text content
			req.Text = bodyText.GoString()
		}

		if !bodyMarkdown.IsNullOrEmpty() {
			// markdown overrides both HTML and text when provided
			// convert markdown to HTML
			markdown := goldmark.New(
				goldmark.WithRendererOptions(
					renderer.WithUnsafe(),
				),
				goldmark.WithExtensions(
					extension.Strikethrough,
					extension.Table,
					extension.Linkify,
				),
			)
			html := bytes.NewBufferString("")
			_ = markdown.Convert([]byte(bodyMarkdown.GoString()), html)
			req.Html = html.String()
			// use original markdown as text
			req.Text = bodyMarkdown.GoString()
		}

		// for attachments
		if fps := attachmentFiles.Slice(); len(fps) > 0 {
			// load file content and attach
			for _, r := range fps {
				fp := r.GoString()
				c, err := ioutil.ReadFile(fp)
				if err != nil {
					return none, err
				}
				n := filepath.Base(fp)
				req.Attachments = append(req.Attachments, &resend.Attachment{
					Filename: n,
					Content:  c,
				})
			}
		}
		if dcts := attachmentContents.Slice(); len(dcts) > 0 {
			// convert dict to attachment and attach
			for _, r := range dcts {
				fn, ok, err := r.Get(starlark.String("name"))
				if !ok || err != nil {
					return none, fmt.Errorf("attachment must have a name")
				}
				ct, ok, err := r.Get(starlark.String("content"))
				if !ok || err != nil {
					return none, fmt.Errorf("attachment must have content")
				}
				req.Attachments = append(req.Attachments, &resend.Attachment{
					Filename: dataconv.StarString(fn),
					Content:  []byte(dataconv.StarString(ct)),
				})
			}
		}

		// send it
		ctx := dataconv.GetThreadContext(thread)
		client := resend.NewClient(resendAPIKey)
		sent, err := client.Emails.SendWithContext(ctx, req)

		// Create response fields with success/error status
		fields := starlark.StringDict{
			"success": starlark.Bool(err == nil),
		}
		if err != nil {
			fields["error"] = starlark.String(err.Error())
			fields["id"] = none
			fields["from"] = none
			fields["to"] = none
			fields["cc"] = none
			fields["bcc"] = none
			fields["reply_to"] = none
			fields["subject"] = none
			fields["body_html"] = none
			fields["body_text"] = none
		} else {
			fields["error"] = none
			fields["id"] = starlark.String(sent.Id)
			fields["from"] = starlark.String(req.From)
			fields["to"] = stringListToStarlark(req.To)
			fields["cc"] = stringListToStarlark(req.Cc)
			fields["bcc"] = stringListToStarlark(req.Bcc)
			fields["reply_to"] = starlark.String(req.ReplyTo)
			fields["subject"] = starlark.String(req.Subject)
			fields["body_html"] = starlark.String(req.Html)
			fields["body_text"] = starlark.String(req.Text)

			// Add attachments if present
			if len(req.Attachments) > 0 {
				attachments := make([]starlark.Value, len(req.Attachments))
				for i, att := range req.Attachments {
					attDict := starlark.NewDict(2)
					attDict.SetKey(starlark.String("name"), starlark.String(att.Filename))
					attDict.SetKey(starlark.String("content"), starlark.Bytes(att.Content))
					attachments[i] = attDict
				}
				fields["attachments"] = starlark.NewList(attachments)
			} else {
				fields["attachments"] = none
			}
		}

		return starlarkstruct.FromStringDict(starlarkstruct.Default, fields), nil
	})
}
