package email

// Tests for the email module.
//
// Sections:
//   - composeRequest validation / address resolution (offline, PKG-12)
//   - send() through the Starlark module (offline)
//   - send() argument parsing / attachment error branches (offline, no network)
//   - secret / generated-builtin contract (offline)
//   - stringListToStarlark helper (pure)
//   - live Resend integration (opt-in: EMAIL_RUN_INTEGRATION + real key)

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/1set/starlet"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// runSend runs a one-statement send(...) script through a module pre-seeded with
// a dummy API key, and returns the error from m.Run(). The dummy key is enough
// to get past send()'s "is the key set?" guard so that the offline branches
// (argument parsing, composeRequest validation, attachment file/dict handling)
// execute. These branches all run BEFORE the network call, so no live request
// is made; only scripts that reach a valid request would touch the network, and
// the cases here deliberately error out first.
func runSend(t *testing.T, senderDomain, callBody string) error {
	t.Helper()
	m := starlet.NewDefault()
	m.SetScriptContent([]byte("load(\"email\",\"send\")\n" + callBody))
	m.SetLazyloadModules(map[string]starlet.ModuleLoader{
		ModuleName: NewModuleWithConfig("re_dummy_offline_key", senderDomain).LoadModule(),
	})
	_, err := m.Run()
	return err
}

// --- composeRequest validation / address resolution --------------------------

func TestComposeRequestValid(t *testing.T) {
	req, err := composeRequest(sendArgs{
		subject:       "Hi",
		bodyHTML:      "<b>hello</b>",
		to:            []string{"a@example.com"},
		senderAddress: "from@example.com",
	}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.From != "from@example.com" || req.Subject != "Hi" || req.Html != "<b>hello</b>" {
		t.Errorf("unexpected request: %+v", req)
	}
}

func TestComposeRequestFromID(t *testing.T) {
	req, err := composeRequest(sendArgs{
		subject: "Hi", bodyText: "hello",
		to:          []string{"a@example.com"},
		fromNameID:  "noreply",
		replyNameID: "support",
	}, "mail.example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.From != "noreply@mail.example.com" {
		t.Errorf("From = %q, want noreply@mail.example.com", req.From)
	}
	if req.ReplyTo != "support@mail.example.com" {
		t.Errorf("ReplyTo = %q, want support@mail.example.com", req.ReplyTo)
	}
}

func TestComposeRequestErrors(t *testing.T) {
	cases := []struct {
		name string
		args sendArgs
		dom  string
		want string
	}{
		{"no body", sendArgs{subject: "s", to: []string{"a@x.com"}, senderAddress: "f@x.com"}, "", "html or text"},
		{"no to", sendArgs{subject: "s", bodyText: "b", senderAddress: "f@x.com"}, "", "to must be set"},
		{"no sender", sendArgs{subject: "s", bodyText: "b", to: []string{"a@x.com"}}, "", "sender or from_id"},
		{"from_id without domain", sendArgs{subject: "s", bodyText: "b", to: []string{"a@x.com"}, fromNameID: "noreply"}, "", "from_id"},
		{"reply_id without domain", sendArgs{subject: "s", bodyText: "b", to: []string{"a@x.com"}, senderAddress: "f@x.com", replyNameID: "support"}, "", "reply_id"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := composeRequest(c.args, c.dom)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want contains %q", err, c.want)
			}
		})
	}
}

func TestResolveAddress(t *testing.T) {
	if got, _ := resolveAddress("direct@x.com", "id", "dom.com", "from_id"); got != "direct@x.com" {
		t.Errorf("direct address should win, got %q", got)
	}
	if got, _ := resolveAddress("", "id", "dom.com", "from_id"); got != "id@dom.com" {
		t.Errorf("built address = %q, want id@dom.com", got)
	}
	if got, _ := resolveAddress("", "", "dom.com", "from_id"); got != "" {
		t.Errorf("empty inputs should yield empty, got %q", got)
	}
	if _, err := resolveAddress("", "id", "", "from_id"); err == nil {
		t.Error("name id without domain should error")
	}
}

func TestComposeRequestRecipientsAndReply(t *testing.T) {
	// A direct reply_to wins over the sender domain, and cc/bcc/multi-to are
	// carried through verbatim and in order.
	req, err := composeRequest(sendArgs{
		subject:       "Report",
		bodyHTML:      "<p>x</p>",
		bodyText:      "x",
		to:            []string{"a@x.com", "b@x.com"},
		cc:            []string{"c@x.com"},
		bcc:           []string{"d@x.com", "e@x.com"},
		senderAddress: "from@x.com",
		replyAddress:  "reply@x.com",
		replyNameID:   "ignored", // direct reply address must win over the name id
	}, "domain.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(req.To, ","); got != "a@x.com,b@x.com" {
		t.Errorf("To = %q, want a@x.com,b@x.com", got)
	}
	if got := strings.Join(req.Cc, ","); got != "c@x.com" {
		t.Errorf("Cc = %q, want c@x.com", got)
	}
	if got := strings.Join(req.Bcc, ","); got != "d@x.com,e@x.com" {
		t.Errorf("Bcc = %q, want d@x.com,e@x.com", got)
	}
	if req.ReplyTo != "reply@x.com" {
		t.Errorf("ReplyTo = %q, want reply@x.com (direct wins)", req.ReplyTo)
	}
	if req.Html != "<p>x</p>" || req.Text != "x" {
		t.Errorf("body not carried through: html=%q text=%q", req.Html, req.Text)
	}
}

func TestComposeRequestEmptyReplyStaysEmpty(t *testing.T) {
	// No reply_to / reply_id at all leaves ReplyTo empty even with a sender
	// domain present; the optional reply address must not be fabricated.
	req, err := composeRequest(sendArgs{
		subject:    "Hi",
		bodyText:   "b",
		to:         []string{"a@x.com"},
		fromNameID: "noreply",
	}, "mail.example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.ReplyTo != "" {
		t.Errorf("ReplyTo = %q, want empty when no reply provided", req.ReplyTo)
	}
	if req.From != "noreply@mail.example.com" {
		t.Errorf("From = %q, want noreply@mail.example.com", req.From)
	}
}

func TestComposeRequestWhitespaceOnlyBodyRejected(t *testing.T) {
	// ystring.IsBlank treats whitespace-only as blank: a body of spaces/newlines
	// is not a valid body, so this must error rather than ship a blank email.
	_, err := composeRequest(sendArgs{
		subject:       "s",
		bodyHTML:      "   \n\t ",
		to:            []string{"a@x.com"},
		senderAddress: "f@x.com",
	}, "")
	if err == nil || !strings.Contains(err.Error(), "html or text") {
		t.Errorf("whitespace-only body should be rejected, got %v", err)
	}
}

func TestResolveAddressDirectWithBlankDomain(t *testing.T) {
	// A direct address needs no domain, even when a name id slot is blank.
	got, err := resolveAddress("direct@x.com", "", "", "from_id")
	if err != nil || got != "direct@x.com" {
		t.Errorf("resolveAddress direct = (%q,%v), want (direct@x.com,nil)", got, err)
	}
}

// --- send() through the Starlark module --------------------------------------

func TestSendRequiresAPIKey(t *testing.T) {
	// NewModule() resolves the key from EMAIL_RESEND_API_KEY when present; clear
	// it so the "key not set" branch is exercised deterministically regardless
	// of the host environment.
	t.Setenv("EMAIL_RESEND_API_KEY", "")
	m := starlet.NewDefault()
	m.SetScriptContent([]byte(`load("email","send")
send(subject="s", text="b", to="a@example.com", sender="f@example.com")`))
	m.SetLazyloadModules(map[string]starlet.ModuleLoader{ModuleName: NewModule().LoadModule()})
	_, err := m.Run()
	if err == nil || !strings.Contains(err.Error(), "resend_api_key") {
		t.Errorf("expected resend_api_key error, got %v", err)
	}
}

func TestSendRejectsMarkdownKeyword(t *testing.T) {
	// The markdown parameter was removed (PKG-12); passing it must error.
	m := starlet.NewDefault()
	m.SetScriptContent([]byte(`load("email","send")
send(subject="s", markdown="# hi", to="a@example.com", sender="f@example.com")`))
	m.SetLazyloadModules(map[string]starlet.ModuleLoader{ModuleName: NewModuleWithConfig("re_dummy", "").LoadModule()})
	_, err := m.Run()
	if err == nil || !strings.Contains(err.Error(), "markdown") {
		t.Errorf("expected unexpected-keyword error for markdown, got %v", err)
	}
}

// --- send() argument parsing / attachment error branches (offline, no network) ---
//
// These drive the send() builtin with a dummy API key set, exercising every
// error path that fires BEFORE the transport call: argument-type/unpack errors,
// composeRequest validation propagated through the builtin, and the two
// attachment paths (host file read, inline dict). No live request is made.

func TestSendArgumentErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			"missing required subject",
			`send(text="b", to="a@x.com", sender="f@x.com")`,
			"missing argument for subject",
		},
		{
			// to is an OneOrMany unpacker, so omitting it leaves it empty rather
			// than tripping UnpackArgs; composeRequest then rejects the empty list.
			"omitted to",
			`send(subject="s", text="b", sender="f@x.com")`,
			"to must be set",
		},
		{
			"to of wrong scalar type",
			`send(subject="s", text="b", to=123, sender="f@x.com")`,
			`for parameter "to"`,
		},
		{
			"to list with a non-string element",
			`send(subject="s", text="b", to=["ok@x.com", 7], sender="f@x.com")`,
			`for parameter "to"`,
		},
		{
			"unknown keyword is rejected",
			`send(subject="s", text="b", to="a@x.com", sender="f@x.com", bogus=1)`,
			"bogus",
		},
		{
			"no body propagated from composeRequest",
			`send(subject="s", to="a@x.com", sender="f@x.com")`,
			"html or text",
		},
		{
			"empty to-list propagated from composeRequest",
			`send(subject="s", text="b", to=[], sender="f@x.com")`,
			"to must be set",
		},
		{
			"no sender and no from_id",
			`send(subject="s", text="b", to="a@x.com")`,
			"sender or from_id",
		},
		{
			"from_id used without a sender domain",
			`send(subject="s", text="b", to="a@x.com", from_id="noreply")`,
			"sender_domain",
		},
		{
			"attachment_file points at a missing path",
			`send(subject="s", text="b", to="a@x.com", sender="f@x.com", attachment_file="/no/such/file/here")`,
			"no such file",
		},
		{
			"inline attachment missing name",
			`send(subject="s", text="b", to="a@x.com", sender="f@x.com", attachment={"content":"x"})`,
			"attachment must have a name",
		},
		{
			"inline attachment missing content",
			`send(subject="s", text="b", to="a@x.com", sender="f@x.com", attachment={"name":"f.txt"})`,
			"attachment must have content",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := runSend(t, "", c.body)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want contains %q", err, c.want)
			}
		})
	}
}

func TestSendAttachmentFileIsDirectory(t *testing.T) {
	// os.ReadFile on a directory returns an error, not a panic; send() must
	// surface it as a clean Starlark error (no host crash, invariant 2).
	dir := t.TempDir()
	err := runSend(t, "", `send(subject="s", text="b", to="a@x.com", sender="f@x.com", attachment_file=`+starlark.String(dir).String()+`)`)
	if err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("reading a directory as an attachment should error, got %v", err)
	}
}

func TestSendAttachmentFileSecondOfTwoMissing(t *testing.T) {
	// The first attachment_file is readable; the second is missing. The whole
	// call must fail on the missing one rather than partially send.
	dir := t.TempDir()
	good := filepath.Join(dir, "ok.txt")
	if e := os.WriteFile(good, []byte("hello"), 0o600); e != nil {
		t.Fatalf("setup: %v", e)
	}
	body := `send(subject="s", text="b", to="a@x.com", sender="f@x.com", attachment_file=[` +
		starlark.String(good).String() + `, "/no/such/second/file"])`
	err := runSend(t, "", body)
	if err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Errorf("a missing second attachment should error, got %v", err)
	}
}

func TestSendFromIDWithoutDomainErrors(t *testing.T) {
	// With a sender domain configured, from_id resolves; without one it errors.
	// The error path runs entirely offline (composeRequest, before transport).
	if err := runSend(t, "", `send(subject="s", text="b", to="a@x.com", reply_id="support", sender="f@x.com")`); err == nil ||
		!strings.Contains(err.Error(), "sender_domain") {
		t.Errorf("reply_id without a domain should error on sender_domain, got %v", err)
	}
}

// --- secret / generated-builtin contract (offline) ---------------------------

// loadModuleSymbols loads the module and returns its exported Starlark members
// (the loader wraps them in a starlarkstruct.Module keyed by the module name).
func loadModuleSymbols(t *testing.T, m *Module) starlark.StringDict {
	t.Helper()
	dict, err := m.LoadModule()()
	if err != nil {
		t.Fatalf("LoadModule loader failed: %v", err)
	}
	mod, ok := dict[ModuleName].(*starlarkstruct.Module)
	if !ok {
		t.Fatalf("loader did not return a module under %q, got %T", ModuleName, dict[ModuleName])
	}
	return mod.Members
}

func TestSecretKeyHasNoGetter(t *testing.T) {
	// Invariant 1: resend_api_key is SetSecret(true), so base generates a
	// set_resend_api_key but NO get_resend_api_key. sender_domain is not secret,
	// so it gets both. send is always present.
	dict := loadModuleSymbols(t, NewModule())
	want := map[string]bool{
		"send":               true,
		"set_resend_api_key": true,
		"set_sender_domain":  true,
		"get_sender_domain":  true,
		"get_resend_api_key": false, // must NOT exist (secret stays write-only)
	}
	for name, shouldExist := range want {
		_, ok := dict[name]
		if ok != shouldExist {
			t.Errorf("symbol %q present=%v, want present=%v", name, ok, shouldExist)
		}
	}
}

func TestSetSecretFromScriptHasNoReadBack(t *testing.T) {
	// A script may set the key but cannot read it back: there is no getter to
	// call. Confirm set_resend_api_key is callable while get_resend_api_key is
	// an undefined name.
	m := starlet.NewDefault()
	m.SetScriptContent([]byte(`load("email", "set_resend_api_key", "get_sender_domain")
set_resend_api_key("re_set_at_runtime")
set_resend_api_key  # referencing it is fine
dom = get_sender_domain()
`))
	m.SetLazyloadModules(map[string]starlet.ModuleLoader{ModuleName: NewModule().LoadModule()})
	res, err := m.Run()
	if err != nil {
		t.Fatalf("setting the secret then reading the domain should work, got %v", err)
	}
	if dom, _ := res["dom"].(string); dom != "" {
		t.Errorf("get_sender_domain() = %q, want empty default", dom)
	}

	// Now prove get_resend_api_key is not a loadable symbol.
	m2 := starlet.NewDefault()
	m2.SetScriptContent([]byte(`load("email", "get_resend_api_key")
`))
	m2.SetLazyloadModules(map[string]starlet.ModuleLoader{ModuleName: NewModule().LoadModule()})
	if _, err := m2.Run(); err == nil {
		t.Error("loading get_resend_api_key should fail: secret options expose no getter")
	}
}

// --- stringListToStarlark helper (pure) --------------------------------------

func TestStringListToStarlark(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", []string{}, []string{}},
		{"single", []string{"a@x.com"}, []string{"a@x.com"}},
		{"multiple keeps order", []string{"a@x.com", "b@x.com", "c@x.com"}, []string{"a@x.com", "b@x.com", "c@x.com"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := stringListToStarlark(c.in)
			lst, ok := v.(*starlark.List)
			if !ok {
				t.Fatalf("stringListToStarlark returned %T, want *starlark.List", v)
			}
			if lst.Len() != len(c.want) {
				t.Fatalf("len = %d, want %d", lst.Len(), len(c.want))
			}
			for i, w := range c.want {
				got, ok := starlark.AsString(lst.Index(i))
				if !ok || got != w {
					t.Errorf("element %d = %v, want %q", i, lst.Index(i), w)
				}
			}
		})
	}
}

// --- live Resend integration (opt-in) ----------------------------------------

func TestSendIntegration(t *testing.T) {
	if os.Getenv("EMAIL_RUN_INTEGRATION") == "" {
		t.Skip("set EMAIL_RUN_INTEGRATION=1 (with EMAIL_RESEND_API_KEY) to run the live Resend send test")
	}
	key := os.Getenv("EMAIL_RESEND_API_KEY")
	if key == "" {
		t.Skip("EMAIL_RESEND_API_KEY not set")
	}
	m := starlet.NewDefault()
	// Resend's onboarding sender + delivered test recipient need no domain setup.
	m.SetScriptContent([]byte(`load("email","send")
out = send(
    subject="starpkg email integration test",
    text="hello from the starpkg email module",
    to="delivered@resend.dev",
    sender="onboarding@resend.dev",
)
ok = out.success
`))
	m.SetLazyloadModules(map[string]starlet.ModuleLoader{ModuleName: NewModuleWithConfig(key, "").LoadModule()})
	res, err := m.Run()
	if err != nil {
		t.Fatalf("integration send failed: %v", err)
	}
	if ok, _ := res["ok"].(bool); !ok {
		t.Errorf("integration send did not succeed: %v", res["out"])
	}
}
