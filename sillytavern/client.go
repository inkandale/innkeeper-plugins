package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"

	"github.com/inkandale/innkeeper-plugins/internal/httpkit"
	"github.com/inkandale/innkeeper/protocol"
)

// client calls one SillyTavern server's API as one user.
type client struct {
	base, auth         string // auth is "none", "basic", or "account"
	username, password string

	mu   sync.Mutex
	sess *session
	// failed is set by a rejected login. SillyTavern allows five logins per IP
	// a minute, so the plugin doesn't try again until it restarts, which a
	// change to the instance's config or secrets does.
	failed error
}

// session is one cookie jar and the CSRF token that goes with it.
type session struct {
	http *http.Client
	csrf string
}

// newClient returns a client for the server at base. An empty auth is none,
// the config's default.
func newClient(base, auth, username, password string) *client {
	return &client{base: strings.TrimRight(base, "/"), auth: cmp.Or(auth, "none"), username: username, password: password}
}

// request is one API call. body is sent as is with contentType.
type request struct {
	method, path string
	body         []byte
	contentType  string
}

// call POSTs a JSON body to path and decodes the reply into out.
func (c *client) call(ctx context.Context, path string, in, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return c.do(ctx, request{method: http.MethodPost, path: path, body: raw, contentType: "application/json"}, out)
}

// upload POSTs a multipart form to path: fields, and file as the field
// "avatar", the only name SillyTavern's upload handler reads.
func (c *client) upload(ctx context.Context, path string, fields map[string]string, file []byte, out any) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			return err
		}
	}
	part, err := mw.CreateFormFile("avatar", "avatar.png")
	if err != nil {
		return err
	}
	_, _ = part.Write(file)
	if err := mw.Close(); err != nil {
		return err
	}
	return c.do(ctx, request{method: http.MethodPost, path: path, body: buf.Bytes(), contentType: mw.FormDataContentType()}, out)
}

// do sends r with a session. A 403 may mean the session has ended, so it logs
// in again once; a 403 on a session made for this call is final.
func (c *client) do(ctx context.Context, r request, out any) error {
	sess, fresh, err := c.session(ctx, nil)
	if err != nil {
		return err
	}
	resp, err := c.send(ctx, sess, r)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusForbidden && !fresh {
		resp.Body.Close()
		if sess, _, err = c.session(ctx, sess); err != nil {
			return err
		}
		if resp, err = c.send(ctx, sess, r); err != nil {
			return err
		}
	}
	defer resp.Body.Close()
	what := r.method + " " + r.path
	if resp.StatusCode == http.StatusForbidden {
		c.drop(sess)
		msg := what + ": SillyTavern answered 403 on a fresh login. Either its whitelist " +
			"doesn't include this server's address, it refused the CSRF token, or the account is disabled"
		if c.auth == "basic" {
			msg += ". With user accounts on, basic mode needs perUserBasicAuth in SillyTavern's config"
		}
		return protocol.Fail(msg, false)
	}
	return httpkit.Decode(resp, what, out)
}

func (c *client) send(ctx context.Context, s *session, r request) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, r.method, c.base+r.path, bytes.NewReader(r.body))
	if err != nil {
		return nil, err
	}
	if r.contentType != "" {
		req.Header.Set("Content-Type", r.contentType)
	}
	if c.auth == "basic" {
		req.SetBasicAuth(c.username, c.password)
	}
	if r.method == http.MethodPost && s.csrf != "" {
		req.Header.Set("X-CSRF-Token", s.csrf)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, protocol.Fail(fmt.Sprintf("%s %s: %v", r.method, r.path, err), true)
	}
	return resp, nil
}

// session returns the current session, logging in if there is none or if it
// is stale. fresh reports that this call made it.
func (c *client) session(ctx context.Context, stale *session) (s *session, fresh bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failed != nil {
		return nil, false, c.failed
	}
	if c.sess != nil && c.sess != stale {
		return c.sess, false, nil
	}
	if c.auth != "none" && c.auth != "basic" && c.auth != "account" {
		return nil, false, protocol.InvalidParams(fmt.Errorf("auth is %q, want none, basic, or account", c.auth))
	}
	if c.base == "" {
		return nil, false, protocol.Fail("set the instance's url config", false)
	}
	if c.auth != "none" && (c.username == "" || c.password == "") {
		return nil, false, protocol.Fail("set the instance's username config and its password secret", false)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, false, err
	}
	s = &session{http: httpkit.NewClient(jar)}
	if err := c.login(ctx, s); err != nil {
		var rej *rejection
		if errors.As(err, &rej) {
			c.failed = protocol.Fail(rej.Error()+". Fix it in the instance's config or secrets, which restarts the plugin.", false)
			return nil, false, c.failed
		}
		return nil, false, err
	}
	c.sess = s
	return s, true, nil
}

// rejection is the server's refusal of the login's credentials. Retrying
// risks its lockout, so only this latches the client.
type rejection struct {
	status int
	step   string
	body   string
}

func (e *rejection) Error() string {
	if e.status == http.StatusUnauthorized {
		return "SillyTavern rejected the username or password at " + e.step
	}
	return "SillyTavern rejected the account login: " + e.body
}

// login makes s a session SillyTavern accepts. For basic auth, GET /login
// binds the session to the account when accounts and perUserBasicAuth are
// on. The CSRF token comes first for an account login, which is itself a
// POST.
func (c *client) login(ctx context.Context, s *session) error {
	step := func(r request, out any) error {
		resp, err := c.send(ctx, s, r)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		forbidden := resp.StatusCode == http.StatusForbidden
		if resp.StatusCode == http.StatusUnauthorized || forbidden && r.method == http.MethodPost {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return &rejection{resp.StatusCode, r.method + " " + r.path, httpkit.Snippet(b)}
		}
		// A 403 before the login is the whitelist. It costs no login attempt and
		// is fixed in SillyTavern's config, so the client may try again.
		if forbidden {
			return protocol.Fail("SillyTavern answered 403 at "+r.path+": add this server's address to "+
				"whitelist in its config.yaml, or turn whitelistMode off", false)
		}
		return httpkit.Decode(resp, "login: "+r.method+" "+r.path, out)
	}
	if c.auth == "basic" {
		if err := step(request{method: http.MethodGet, path: "/login"}, nil); err != nil {
			return err
		}
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := step(request{method: http.MethodGet, path: "/csrf-token"}, &tok); err != nil {
		return err
	}
	s.csrf = tok.Token
	if c.auth != "account" {
		return nil
	}
	raw, _ := json.Marshal(map[string]string{"handle": c.username, "password": c.password})
	return step(request{method: http.MethodPost, path: "/api/users/login", body: raw, contentType: "application/json"}, nil)
}

// drop forgets s, unless another call has already replaced it.
func (c *client) drop(s *session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sess == s {
		c.sess = nil
	}
}
