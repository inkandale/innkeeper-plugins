package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/inkandale/innkeeper-plugins/internal/httpkit"
	"github.com/inkandale/innkeeper/protocol"
)

// newTestClient connects to f in the given mode, with the credentials f expects.
func newTestClient(f *fake, auth string) *client {
	switch auth {
	case "basic":
		f.basicUser, f.basicPass = "owner", fakePassword
	case "account":
		f.accounts = true
	}
	return newClient(f.srv.URL+"/", auth, "owner", fakePassword)
}

func list(c *client) error {
	return c.call(context.Background(), "/api/worldinfo/list", map[string]any{}, nil)
}

func final(t *testing.T, err error) *protocol.Error {
	t.Helper()
	var pe *protocol.Error
	if !errors.As(err, &pe) || pe.Retryable() {
		t.Fatalf("got %v, want a final error", err)
	}
	return pe
}

func retryable(t *testing.T, err error) {
	t.Helper()
	var pe *protocol.Error
	if !errors.As(err, &pe) || !pe.Retryable() {
		t.Fatalf("got %v, want a retryable error", err)
	}
}

func TestLogsInOnceAndReusesTheSession(t *testing.T) {
	for _, auth := range []string{"basic", "account"} {
		t.Run(auth, func(t *testing.T) {
			f := newFake(t)
			f.accounts, f.perUserBasic = true, true // GET /login then picks the account for basic auth
			c := newTestClient(f, auth)
			for range 3 {
				if err := list(c); err != nil {
					t.Fatal(err)
				}
			}
			if f.logins != 1 || f.csrfTokens != 1 {
				t.Fatalf("%d logins and %d CSRF tokens, want 1 of each", f.logins, f.csrfTokens)
			}
		})
	}
}

func TestAuthNoneNeedsNoLogin(t *testing.T) {
	f := newFake(t)
	if err := list(newClient(f.srv.URL, "none", "", "")); err != nil || f.logins != 0 {
		t.Fatalf("%v, %d logins", err, f.logins)
	}
}

// The form defaults Login to none and leaves a default out of the config.
func TestAuthLeftOutMeansNone(t *testing.T) {
	f := newFake(t)
	if err := list(newClient(f.srv.URL, "", "", "")); err != nil || f.logins != 0 {
		t.Fatalf("%v, %d logins", err, f.logins)
	}
}

func TestSendsTheCSRFToken(t *testing.T) {
	f := newFake(t)
	c := newTestClient(f, "account")
	if err := list(c); err != nil {
		t.Fatal(err)
	}
	if f.lastCSRF == "" || f.lastCSRF == "disabled" {
		t.Fatalf("X-CSRF-Token = %q, want the server's token", f.lastCSRF)
	}
}

func TestSendsADisabledToken(t *testing.T) {
	f := newFake(t)
	f.csrfDisabled = true
	if err := list(newTestClient(f, "account")); err != nil || f.lastCSRF != "disabled" {
		t.Fatalf("%v, X-CSRF-Token = %q", err, f.lastCSRF)
	}
}

func TestGetsANewSessionOnceAfterA403(t *testing.T) {
	f := newFake(t)
	c := newTestClient(f, "account")
	if err := list(c); err != nil {
		t.Fatal(err)
	}
	f.endSessions()
	if err := list(c); err != nil || f.logins != 2 {
		t.Fatalf("after the session ended: %v, %d logins", err, f.logins)
	}
}

func TestASecond403IsFinalAndNamesTheWhitelist(t *testing.T) {
	f := newFake(t)
	c := newTestClient(f, "account")
	if err := list(c); err != nil {
		t.Fatal(err)
	}
	f.deny = true
	pe := final(t, list(c))
	if f.logins != 2 || !strings.Contains(pe.Message, "whitelist") {
		t.Fatalf("%d logins, error %q", f.logins, pe.Message)
	}
}

func TestStopsAfterARejectedLogin(t *testing.T) {
	cases := []struct {
		name, auth, want string
		spoil            func(*fake)
	}{
		{"wrong account password", "account", "Incorrect credentials", func(f *fake) { f.password = "other" }},
		{"wrong basic password", "basic", "username or password", func(f *fake) { f.basicPass = "other" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			c := newTestClient(f, tc.auth)
			tc.spoil(f)
			pe := final(t, list(c))
			if !strings.Contains(pe.Message, tc.want) {
				t.Fatalf("error %q should say %q", pe.Message, tc.want)
			}
			if strings.Contains(pe.Message, "whitelist") {
				t.Fatalf("error %q names the whitelist, which isn't a possible cause here", pe.Message)
			}
			sent := f.requests
			final(t, list(c))
			if f.requests != sent || f.logins != 0 {
				t.Fatalf("sent %d more requests after the rejection", f.requests-sent)
			}
		})
	}
}

// A refused address costs no login attempt and is fixed in SillyTavern's
// config, so the next call tries again.
func TestAWhitelistRefusalDoesNotStopTheClient(t *testing.T) {
	for _, auth := range []string{"basic", "account"} {
		t.Run(auth, func(t *testing.T) {
			f := newFake(t)
			f.perUserBasic = true
			c := newTestClient(f, auth)
			f.refuse = true
			pe := final(t, list(c))
			if !strings.Contains(pe.Message, "whitelist") {
				t.Fatalf("error %q should name the whitelist", pe.Message)
			}
			f.refuse = false
			if err := list(c); err != nil {
				t.Fatalf("after the address was listed: %v", err)
			}
		})
	}
}

func TestBasicWithAccountsNeedsPerUserBasicAuth(t *testing.T) {
	f := newFake(t)
	f.accounts = true
	pe := final(t, list(newTestClient(f, "basic")))
	if !strings.Contains(pe.Message, "perUserBasicAuth") {
		t.Fatalf("error %q should name perUserBasicAuth", pe.Message)
	}
}

func TestARedirectIsFinalAndNamesTheTarget(t *testing.T) {
	// Only the POST redirects, as an http-to-https rule in front of the API would for a call it can't serve.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"token":"t"}`)
			return
		}
		http.Redirect(w, r, "https://st.example.com"+r.URL.Path, http.StatusMovedPermanently)
	}))
	defer proxy.Close()
	pe := final(t, list(newClient(proxy.URL, "none", "", "")))
	if !strings.Contains(pe.Message, "https://st.example.com/api/worldinfo/list") {
		t.Fatalf("error %q", pe.Message)
	}
}

func TestConcurrentCallsShareOneLogin(t *testing.T) {
	f := newFake(t)
	c := newTestClient(f, "account")
	burst := func() {
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := list(c); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
	}
	burst()
	if f.logins != 1 {
		t.Fatalf("%d logins, want 1", f.logins)
	}
	f.endSessions()
	burst()
	if f.logins != 2 {
		t.Fatalf("%d logins after the sessions ended, want 2", f.logins)
	}
}

// Only a rejection by the server's login counts toward its lockout, so
// anything else may be tried again.
func TestAnOddLoginFailureDoesNotStopTheClient(t *testing.T) {
	f := newFake(t)
	c := newTestClient(f, "account")
	f.csrfBody = "<html>not json</html>"
	final(t, list(c))
	f.csrfBody = ""
	if err := list(c); err != nil {
		t.Fatalf("after the server recovered: %v", err)
	}
}

func TestAFinal403DropsTheSession(t *testing.T) {
	f := newFake(t)
	f.deny = true
	c := newTestClient(f, "account")
	final(t, list(c))
	sent := f.requests
	final(t, list(c))
	// CSRF token, login, one try; no wasted try on the dropped session.
	if f.logins != 2 || f.requests-sent != 3 {
		t.Fatalf("%d logins, %d requests for the second call, want 2 and 3", f.logins, f.requests-sent)
	}
}

func TestRejectsAnUnknownAuthMode(t *testing.T) {
	f := newFake(t)
	for _, auth := range []string{"token", "None"} {
		pe := final(t, list(newClient(f.srv.URL, auth, "owner", fakePassword)))
		if !strings.Contains(pe.Message, "auth") || f.requests != 0 {
			t.Fatalf("auth %q: %q after %d requests", auth, pe.Message, f.requests)
		}
	}
}

func TestARateLimitedLoginCanBeRetried(t *testing.T) {
	f := newFake(t)
	c := newTestClient(f, "account")
	f.loginAttempts = 5
	retryable(t, list(c))
	f.loginAttempts = 0
	if err := list(c); err != nil {
		t.Fatal(err)
	}
}

func TestErrors(t *testing.T) {
	f := newFake(t)
	c := newTestClient(f, "account")
	for status, want := range map[int]string{429: "retryable", 500: "retryable", 503: "retryable", 400: "final", 413: "final"} {
		f.failWith = status
		if want == "retryable" {
			retryable(t, list(c))
		} else {
			final(t, list(c))
		}
	}
	f.failWith = 0
	err := c.call(context.Background(), "/api/characters/get", map[string]any{"avatar_url": "Nobody.png"}, nil)
	if !httpkit.NotFound(err) {
		t.Fatalf("missing character: %v, want -32004", err)
	}
	if f.logins != 1 {
		t.Fatalf("%d logins, want 1", f.logins)
	}
	f.srv.Close()
	retryable(t, list(c))
}

func TestAnOversizedReplyIsFinal(t *testing.T) {
	defer func(n int64) { httpkit.MaxReply = n }(httpkit.MaxReply)
	// Room for the login's replies, not the list's.
	httpkit.MaxReply = 32
	f := newFake(t)
	f.worlds["Dwarves"] = map[string]any{"entries": map[string]any{}}
	pe := final(t, newTestClient(f, "account").call(context.Background(), "/api/worldinfo/list", map[string]any{}, &[]any{}))
	if !strings.Contains(pe.Message, "/api/worldinfo/list: reply over 32 bytes") {
		t.Fatalf("error %q should say the reply is too big", pe.Message)
	}
}
