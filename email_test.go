package email

// Tests for the email module.
//
// Sections:
//   - composeRequest validation / address resolution (offline, PKG-12)
//   - send() through the Starlark module (offline)
//   - live Resend integration (opt-in: EMAIL_RUN_INTEGRATION + real key)

import (
	"os"
	"strings"
	"testing"

	"github.com/1set/starlet"
)

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

// --- send() through the Starlark module --------------------------------------

func TestSendRequiresAPIKey(t *testing.T) {
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
