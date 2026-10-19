package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/inkandale/innkeeper-plugins/internal/httpkit"
	"github.com/inkandale/innkeeper/protocol"
)

// client calls one Lumiverse server's API as one user.
type client struct {
	base               string
	username, password string
	http               *http.Client

	mu    sync.Mutex
	token string
	// failed is set by a rejected sign-in. Five rejections lock the server's
	// IP out of Lumiverse, so the plugin doesn't sign in again until it
	// restarts, which a change to the instance's config or secrets does.
	failed error
}

func newClient(base, username, password string) *client {
	return &client{base: strings.TrimRight(base, "/"), username: username, password: password,
		http: httpkit.NewClient(nil)}
}

// request is one API call. body is sent as is with contentType.
type request struct {
	method, path string
	body         []byte
	contentType  string
}

func jsonRequest(method, path string, v any) (request, error) {
	r := request{method: method, path: path}
	if v != nil {
		raw, err := json.Marshal(v)
		if err != nil {
			return r, err
		}
		r.body, r.contentType = raw, "application/json"
	}
	return r, nil
}

// call sends a JSON request to path under /api/v1 and decodes the reply into out.
func (c *client) call(ctx context.Context, method, path string, in, out any) error {
	r, err := jsonRequest(method, path, in)
	if err != nil {
		return err
	}
	return c.do(ctx, r, out)
}

// do sends r with a session, signing in again once if the server says the
// session has ended.
func (c *client) do(ctx context.Context, r request, out any) error {
	token, err := c.session(ctx, "")
	if err != nil {
		return err
	}
	resp, err := c.send(ctx, r, token)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		if token, err = c.session(ctx, token); err != nil {
			return err
		}
		if resp, err = c.send(ctx, r, token); err != nil {
			return err
		}
	}
	defer resp.Body.Close()
	return httpkit.Decode(resp, r.method+" "+r.path, out)
}

func (c *client) send(ctx context.Context, r request, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, r.method, c.base+"/api/v1"+r.path, bytes.NewReader(r.body))
	if err != nil {
		return nil, err
	}
	if r.contentType != "" {
		req.Header.Set("Content-Type", r.contentType)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, protocol.Fail(fmt.Sprintf("%s %s: %v", r.method, r.path, err), true)
	}
	return resp, nil
}

// session returns a session token, signing in if there is none or if the
// current one is stale.
func (c *client) session(ctx context.Context, stale string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failed != nil {
		return "", c.failed
	}
	if c.token != "" && c.token != stale {
		return c.token, nil
	}
	if c.base == "" || c.username == "" || c.password == "" {
		return "", protocol.Fail("set the instance's url and username config and its password secret", false)
	}
	body := map[string]string{"username": c.username, "password": c.password}
	path := "/api/auth/sign-in/username"
	if strings.Contains(c.username, "@") {
		body = map[string]string{"email": c.username, "password": c.password}
		path = "/api/auth/sign-in/email"
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", protocol.Fail("sign in: "+err.Error(), true)
	}
	defer resp.Body.Close()
	var reply struct {
		Token   string `json:"token"`
		Session struct {
			Token string `json:"token"`
		} `json:"session"`
	}
	if err := httpkit.Decode(resp, "sign in", &reply); err != nil {
		var pe *protocol.Error
		if errors.As(err, &pe) && pe.Retryable() {
			return "", err
		}
		c.failed = protocol.Fail("Lumiverse rejected the sign-in; fix the username or password, "+
			"which restarts the plugin. Five rejections lock this server out of Lumiverse for a while. "+err.Error(), false)
		return "", c.failed
	}
	// The bearer plugin's header carries the token it expects back.
	c.token = cmp.Or(resp.Header.Get("set-auth-token"), reply.Token, reply.Session.Token)
	if c.token == "" {
		return "", protocol.Fail("sign in: the reply has no token", false)
	}
	return c.token, nil
}
