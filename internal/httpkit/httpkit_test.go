package httpkit

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

// call sends a request with NewClient and decodes the reply into out.
func call(t *testing.T, method, url string, out any) error {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := NewClient(nil).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return Decode(resp, method+" "+req.URL.Path, out)
}

func protocolError(t *testing.T, err error) *protocol.Error {
	t.Helper()
	var pe *protocol.Error
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want a protocol error", err)
	}
	return pe
}

func TestDecodeRefusesAReplyOverTheCap(t *testing.T) {
	defer func(n int64) { MaxReply = n }(MaxReply)
	MaxReply = 8
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"name":"too long"}`)
	}))
	defer srv.Close()
	pe := protocolError(t, call(t, http.MethodGet, srv.URL+"/big", &map[string]any{}))
	if pe.Code != protocol.CodeFailed || pe.Retryable() || !strings.Contains(pe.Message, "reply over 8 bytes") {
		t.Fatalf("err = %v, want a final error naming the cap", pe)
	}
}

func TestRedirectToAnotherHostIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://app.example.com"+r.URL.Path, http.StatusMovedPermanently)
	}))
	defer srv.Close()
	pe := protocolError(t, call(t, http.MethodPost, srv.URL+"/api/list", nil))
	if pe.Retryable() || !strings.Contains(pe.Message, "redirects to https://app.example.com/api/list") {
		t.Fatalf("err = %v, want a final error naming the target", pe)
	}
}

func TestRedirectOnTheSameHostIsFollowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusTemporaryRedirect)
			return
		}
		_, _ = io.WriteString(w, `{"path":"`+r.URL.Path+`"}`)
	}))
	defer srv.Close()
	var out struct{ Path string }
	if err := call(t, http.MethodPost, srv.URL+"/old", &out); err != nil || out.Path != "/new" {
		t.Fatalf("path = %q, err = %v, want /new", out.Path, err)
	}
}

func TestDecodeMapsStatus(t *testing.T) {
	cases := []struct {
		status    int
		code      int
		retryable bool
	}{
		{http.StatusNotFound, protocol.CodeRemoteNotFound, false},
		{http.StatusBadGateway, protocol.CodeFailed, true},
		{http.StatusTooManyRequests, protocol.CodeFailed, true},
		{http.StatusBadRequest, protocol.CodeFailed, false},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
		}))
		pe := protocolError(t, call(t, http.MethodGet, srv.URL+"/x", nil))
		srv.Close()
		if pe.Code != c.code || pe.Retryable() != c.retryable {
			t.Errorf("status %d: err = %v, want code %d retryable %v", c.status, pe, c.code, c.retryable)
		}
	}
}

// unusedPort is a loopback port nothing listens on.
func unusedPort(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	return srv.URL[strings.LastIndex(srv.URL, ":")+1:]
}

func TestNewClientExplainsAnUnreachableLoopbackAddress(t *testing.T) {
	_, err := NewClient(nil).Get("http://localhost:" + unusedPort(t) + "/")
	if err == nil || !strings.Contains(err.Error(), "host.docker.internal") {
		t.Fatalf("err = %v, want the host.docker.internal hint", err)
	}
}

func TestNewClientLeavesOtherFailuresAlone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer srv.Close()
	_, err := NewClient(nil).Get(srv.URL)
	if err == nil || strings.Contains(err.Error(), "host.docker.internal") {
		t.Fatalf("err = %v, want a failure without the hint", err)
	}
}
