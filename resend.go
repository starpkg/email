// Package email provides a Starlark module that sends email using Resend API.
package email

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"path/filepath"

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
)

// ModuleName defines the expected name for this module when used in Starlark's load() function, e.g., load('email', 'send')
const ModuleName = "email"

// Configuration key constants
const (
	configKeyResendAPIKey = "resend_api_key"
	configKeySenderDomain = "sender_domain"
)

// none is a convenience variable for starlark.None
var none = starlark.None

// Module wraps the ConfigurableModule with specific functionality for sending emails.
type Module struct {
	cfgMod *base.ConfigurableModule
}

// NewModule creates a new instance of Module with default empty configurations.
func NewModule() *Module {
	cm, _ := base.NewConfigurableModuleWithOptions(
		base.WithConfigValue(configKeyResendAPIKey, ""),
		base.WithConfigValue(configKeySenderDomain, ""),
	)
	return &Module{cfgMod: cm}
}

// NewModuleWithConfig creates a new instance of Module with the given configuration values.
func NewModuleWithConfig(resendAPIKey, senderDomain string) *Module {
	cm, _ := base.NewConfigurableModuleWithOptions(
		base.WithConfigValue(configKeyResendAPIKey, resendAPIKey),
		base.WithConfigValue(configKeySenderDomain, senderDomain),
	)
	return &Module{cfgMod: cm}
}

// NewModuleWithGetter creates a new instance of Module with the given configuration getters.
func NewModuleWithGetter(resendAPIKey, senderDomain func() string) *Module {
	cm, _ := base.NewConfigurableModuleWithOptions(
		base.WithConfigGetter(configKeyResendAPIKey, resendAPIKey),
		base.WithConfigGetter(configKeySenderDomain, senderDomain),
	)
	return &Module{cfgMod: cm}
}

// LoadModule returns the Starlark module loader with the email-specific functions.
func (m *Module) LoadModule() starlet.ModuleLoader {
	additionalFuncs := starlark.StringDict{
		"send": m.genSendFunc(),
	}
	return m.cfgMod.LoadModule(ModuleName, additionalFuncs)
}

// genSendFunc generates the Starlark callable function to send an email.
func (m *Module) genSendFunc() starlark.Callable {
	return starlark.NewBuiltin(ModuleName+".send", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		// Load config: resend_api_key is required, sender_domain is optional
		resendAPIKey, err := base.GetConfigValue[string](m.cfgMod, configKeyResendAPIKey)
		if err != nil {
			return none, fmt.Errorf("%s is not set", configKeyResendAPIKey)
		}
		senderDomain, err := base.GetConfigValue[string](m.cfgMod, configKeySenderDomain)
		if err != nil {
			return none, fmt.Errorf("%s is not set", configKeySenderDomain)
		}

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
			fromAddress        types.StringOrBytes // one of the two must be set
			fromNameID         types.StringOrBytes
			replyAddress       types.StringOrBytes // two of them are optional
			replyNameID        types.StringOrBytes
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
		if from := []string{fromAddress.GoString(), fromNameID.GoString()}; lo.EveryBy(from, ystring.IsBlank) {
			return none, fmt.Errorf("one of from or from_id must be non-blank")
		}

		// convert from to send address
		var sendAddr string
		if fa := fromAddress.GoString(); ystring.IsNotBlank(fa) {
			sendAddr = fa
		} else if fi := fromNameID.GoString(); ystring.IsNotBlank(fi) {
			if ystring.IsNotBlank(senderDomain) {
				sendAddr = fi + "@" + senderDomain
			} else {
				return none, fmt.Errorf("%s should be set when from_id is used", configKeySenderDomain)
			}
		} else {
			return none, fmt.Errorf("no valid from or from_id found")
		}

		// convert from to reply address
		var replyAddr string
		if ra := replyAddress.GoString(); ystring.IsNotBlank(ra) {
			replyAddr = ra
		} else if ri := replyNameID.GoString(); ystring.IsNotBlank(ri) {
			if ystring.IsNotBlank(senderDomain) {
				replyAddr = ri + "@" + senderDomain
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
		} else if !bodyText.IsNullOrEmpty() {
			// directly use text content
			req.Text = bodyText.GoString()
		} else if !bodyMarkdown.IsNullOrEmpty() {
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
		if err != nil {
			return none, err
		}
		return starlark.String(sent.Id), nil
	})
}
