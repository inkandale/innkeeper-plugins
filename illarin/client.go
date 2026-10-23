package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/inkandale/innkeeper-plugins/internal/httpkit"
	"github.com/inkandale/innkeeper/protocol"
)

// acceptedFormats are the Illarin formats card.Decode and card.DecodeLorebook read, best first.
var acceptedFormats = []string{"chara_card_v3", "chara_card_v2", "charx", "lorebook_sillytavern"}

const receivePermission = "work:receive"

// client calls one Illarin server's connected-app API.
type client struct {
	http  *http.Client
	base  string
	store *store
}

// errNotConnected means there are no credentials, or Illarin refused them; an import connects again.
var errNotConnected = protocol.Fail("not connected to Illarin; run the import again to connect", false)

// do sends a JSON request. token is "" for the connect calls, which carry their own codes.
func (c *client) do(ctx context.Context, method, path, token string, body any) (*http.Response, error) {
	var buf []byte
	if body != nil {
		var err error
		if buf, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, protocol.Fail(fmt.Sprintf("Illarin %s: %v", path, err), true)
	}
	return resp, nil
}

// authed sends a request with the access token, refreshing it once after a 401.
func (c *client) authed(ctx context.Context, method, path string, body any) (*http.Response, error) {
	token, err := c.token(ctx, false)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, method, path, token, body)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	resp.Body.Close()
	if token, err = c.token(ctx, true); err != nil {
		return nil, err
	}
	return c.do(ctx, method, path, token, body)
}

// credentials is a successful poll or refresh.
type credentials struct {
	AccessToken          string    `json:"accessToken"`
	AccessTokenExpiresAt time.Time `json:"accessTokenExpiresAt"`
	RefreshToken         string    `json:"refreshToken"`
	ConnectedApp         struct {
		Permissions []string `json:"permissions"`
	} `json:"connectedApp"`
}

func (c *client) connection(cr credentials) connection {
	return connection{base: c.base, access: cr.AccessToken, accessExpires: cr.AccessTokenExpiresAt, refresh: cr.RefreshToken}
}

// checkGrant fails when the owner left work:receive off; retrying can't fix that.
func checkGrant(cr credentials) error {
	if slices.Contains(cr.ConnectedApp.Permissions, receivePermission) {
		return nil
	}
	return protocol.Fail("Illarin connected without "+receivePermission+"; turn on receiving works for Innkeeper in Illarin's account settings", false)
}

// token returns a live access token. force refreshes even one that hasn't expired.
func (c *client) token(ctx context.Context, force bool) (string, error) {
	var token string
	err := pgx.BeginFunc(ctx, c.store.pool, func(tx pgx.Tx) error {
		conn, ok, err := c.store.lock(ctx, tx)
		if err != nil || !ok {
			return err
		}
		if conn.base != c.base {
			return c.store.forget(ctx, tx)
		}
		if !force && time.Until(conn.accessExpires) > time.Minute {
			token = conn.access
			return nil
		}
		cr, err := c.refresh(ctx, conn.refresh)
		if errors.Is(err, errNotConnected) {
			// Commit the forget so the next run connects again.
			return c.store.forget(ctx, tx)
		}
		if err != nil {
			return err
		}
		token = cr.AccessToken
		return c.store.save(ctx, tx, c.connection(cr))
	})
	if err == nil && token == "" {
		err = errNotConnected
	}
	return token, err
}

// refresh spends the refresh token. Any answer but success or a rate limit
// may have spent it, and Illarin revokes the app if a spent one is sent again.
func (c *client) refresh(ctx context.Context, token string) (credentials, error) {
	var cr credentials
	resp, err := c.do(ctx, http.MethodPost, "/api/v1/connect/refresh", "", map[string]string{"refreshToken": token})
	if err != nil {
		// ponytail: a failed dial never reached Illarin, but telling it from a lost reply isn't worth it; reconnecting is cheap.
		return cr, errNotConnected
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return cr, protocol.Fail("Illarin is rate limiting refreshes", true)
	}
	if resp.StatusCode != http.StatusOK || httpkit.Decode(resp, "Illarin refresh", &cr) != nil {
		return cr, errNotConnected
	}
	return cr, nil
}

// deviceRequest is Illarin's answer to starting a connection.
type deviceRequest struct {
	DeviceCode      string    `json:"deviceCode"`
	UserCode        string    `json:"userCode"`
	VerificationURL string    `json:"verificationUrl"`
	ExpiresAt       time.Time `json:"expiresAt"`
	Interval        int       `json:"interval"`
}

// connect runs the device flow: it tells the owner the code through say,
// waits for their decision, and saves the credentials.
func (c *client) connect(ctx context.Context, say func(string) error) error {
	resp, err := c.do(ctx, http.MethodPost, "/api/v1/connect/requests", "", map[string]any{
		"appName": "Innkeeper", "name": "Innkeeper", "protocolVersion": 1,
		"capabilities": []string{}, "acceptedFormats": acceptedFormats, "permissions": []string{receivePermission},
	})
	if err != nil {
		return err
	}
	var dr deviceRequest
	if err := httpkit.Decode(resp, "Illarin connect", &dr); err != nil {
		resp.Body.Close()
		return err
	}
	resp.Body.Close()
	err = say(fmt.Sprintf("To connect Innkeeper, open %s, check that it shows the code %s, and allow receiving works. The code expires at %s.",
		dr.VerificationURL, dr.UserCode, dr.ExpiresAt.Local().Format(time.Kitchen)))
	if err != nil {
		return err
	}
	wait := time.Duration(max(dr.Interval, 1)) * time.Second
	for {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
		cr, next, err := c.poll(ctx, dr.DeviceCode, wait)
		if err != nil {
			return err
		}
		if next > 0 {
			wait = next
			continue
		}
		if err := checkGrant(cr); err != nil {
			return err
		}
		return c.store.save(ctx, c.store.pool, c.connection(cr))
	}
}

// poll asks once whether the owner decided. next is how long to wait before
// asking again, or 0 when cr holds the new credentials.
func (c *client) poll(ctx context.Context, deviceCode string, wait time.Duration) (cr credentials, next time.Duration, err error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/v1/connect/poll", "", map[string]string{"deviceCode": deviceCode})
	// A network failure leaves the decision unknown; back off and ask again.
	var perr *protocol.Error
	if errors.As(err, &perr) && perr.Retryable() {
		return cr, 2 * wait, nil
	}
	if err != nil {
		return cr, 0, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		var body struct {
			Status string `json:"status"`
			credentials
		}
		if err := httpkit.Decode(resp, "Illarin poll", &body); err != nil {
			return cr, 0, err
		}
		if body.Status != "connected" {
			return cr, wait, nil
		}
		return body.credentials, 0, nil
	case http.StatusTooManyRequests:
		// slow_down raises the interval for the rest of the request.
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && time.Duration(s)*time.Second > wait {
			return cr, time.Duration(s) * time.Second, nil
		}
		return cr, wait + 5*time.Second, nil
	case http.StatusBadRequest, http.StatusNotFound:
		return cr, 0, protocol.Fail("Illarin connection not approved: the code was declined, expired, or already used; run the import again for a new code", false)
	}
	err = httpkit.Decode(resp, "Illarin poll", nil)
	if errors.As(err, &perr) && perr.Retryable() {
		return cr, 2 * wait, nil
	}
	return cr, 0, err
}
