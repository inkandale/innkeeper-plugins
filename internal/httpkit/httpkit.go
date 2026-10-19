// Package httpkit has the JSON-over-HTTP client code the app targets share:
// redirect handling, a reply size cap, and mapping replies to protocol errors.
package httpkit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/inkandale/innkeeper/protocol"
)

// MaxReply caps a reply's size; a variable so tests can lower it.
var MaxReply int64 = 64 << 20

// NewClient returns a client for an app's API that keeps cookies in jar,
// which may be nil.
func NewClient(jar http.CookieJar) *http.Client {
	return &http.Client{Jar: jar, Timeout: time.Minute, CheckRedirect: sameHostSameMethod}
}

// sameHostSameMethod follows a redirect only where the call survives it. A
// proxy's http-to-https 301 turns a POST into a GET, which an app may answer
// with a 404 that would read as a missing item. Decode reports the rest.
func sameHostSameMethod(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	last := via[len(via)-1]
	if req.URL.Scheme == last.URL.Scheme && req.URL.Host == last.URL.Host && req.Method == last.Method {
		return nil
	}
	return http.ErrUseLastResponse
}

// Decode maps a reply's status to a protocol error, or decodes its body
// into out; an out of type *[]byte gets the body as it is. what names the
// call in error messages.
func Decode(resp *http.Response, what string, out any) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxReply+1))
	if err != nil {
		return protocol.Fail(what+": "+err.Error(), true)
	}
	if int64(len(body)) > MaxReply {
		return protocol.Fail(fmt.Sprintf("%s: reply over %d bytes", what, MaxReply), false)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return protocol.NewError(protocol.CodeRemoteNotFound, what+": not found", false)
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		return protocol.Fail(fmt.Sprintf("%s: %s: %s", what, resp.Status, Snippet(body)), true)
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return protocol.Fail(fmt.Sprintf("%s: the url redirects to %s; set url to the final address",
			what, resp.Header.Get("Location")), false)
	case resp.StatusCode >= 300:
		return protocol.Fail(fmt.Sprintf("%s: %s: %s", what, resp.Status, Snippet(body)), false)
	}
	if out == nil {
		return nil
	}
	if raw, ok := out.(*[]byte); ok {
		*raw = body
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return protocol.Fail(what+": bad reply: "+err.Error(), false)
	}
	return nil
}

// Snippet is the start of a reply body, short enough for an error message.
func Snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// NotFound reports whether err is the app not having the item.
func NotFound(err error) bool {
	var pe *protocol.Error
	return errors.As(err, &pe) && pe.Code == protocol.CodeRemoteNotFound
}
