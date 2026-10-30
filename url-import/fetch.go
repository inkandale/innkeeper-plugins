package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/inkandale/innkeeper-plugins/internal/pluginio"
	"github.com/inkandale/innkeeper/protocol"
)

// maxBody caps each response. RisuRealm's PNGs with embedded assets run past 10 MiB.
const maxBody = pluginio.MaxInput

// fetcher makes the plugin's requests to sites.
type fetcher struct {
	http *http.Client
	gap  time.Duration // the least time between two requests to one host
	// pause is how long a host that answers 429 without a Retry-After is left alone.
	pause time.Duration

	mu   sync.Mutex
	next map[string]time.Time // when each host may next be asked
}

func newFetcher() *fetcher {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: refusePrivate}).DialContext
	return &fetcher{http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: httpsOnly, Transport: tr}, gap: time.Second, pause: 30 * time.Second}
}

// rateTries is how many times get asks a host that keeps answering 429.
const rateTries = 3

// maxPause caps the Retry-After a host can ask for.
const maxPause = 10 * time.Minute

// wait holds a request to host until gap has passed since the last one.
// ponytail: one gap for every site and none for redirects; per-site gaps if a site asks for more.
func (f *fetcher) wait(ctx context.Context, host string) error {
	f.mu.Lock()
	at := time.Now()
	if n := f.next[host]; n.After(at) {
		at = n
	}
	if f.next == nil {
		f.next = map[string]time.Time{}
	}
	f.next[host] = at.Add(f.gap)
	f.mu.Unlock()
	select {
	case <-time.After(time.Until(at)):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// backOff holds every request to host for d, so one 429 slows them all
// instead of each request tripping the limit again.
func (f *fetcher) backOff(host string, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.next == nil {
		f.next = map[string]time.Time{}
	}
	if at := time.Now().Add(min(d, maxPause)); at.After(f.next[host]) {
		f.next[host] = at
	}
}

// retryAfter is a Retry-After header's delay in seconds, or fallback.
// ponytail: the HTTP-date form falls back too; parse it if a site sends one.
func retryAfter(h string, fallback time.Duration) time.Duration {
	if s, err := strconv.Atoi(h); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	return fallback
}

var errPrivate = errors.New("refused to connect to a private address")

// refusePrivate is a net.Dialer Control that stops a site's redirect or
// avatar URL from reaching loopback, private, or link-local hosts. It runs
// on the resolved IP, so DNS tricks don't get around it.
func refusePrivate(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return err
	}
	ip := ap.Addr().Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return errPrivate
	}
	return nil
}

// httpsOnly is a CheckRedirect that refuses a redirect off https.
func httpsOnly(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return http.ErrUseLastResponse // get reports the 3xx
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

// get fetches url, which must be https, including any URL a site's JSON
// names. site names the site in errors. A 429 holds the host and asks again
// up to rateTries times; then it, a 5xx, or a network error is retryable.
func (f *fetcher) get(ctx context.Context, site, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, permanent(fmt.Errorf("%s sent a bad URL: %w", site, err))
	}
	if req.URL.Scheme != "https" {
		return nil, permanent(fmt.Errorf("%s: refusing non-https URL %s", site, req.URL.Redacted()))
	}
	// chub.ai blocks curl/ agents, and JanitorAI blocks Go's default.
	req.Header.Set("User-Agent", "innkeeper-url-import")
	var resp *http.Response
	for try := 1; ; try++ {
		if err := f.wait(ctx, req.URL.Host); err != nil {
			return nil, err
		}
		resp, err = f.http.Do(req)
		if err != nil || resp.StatusCode != http.StatusTooManyRequests {
			break
		}
		f.backOff(req.URL.Host, retryAfter(resp.Header.Get("Retry-After"), f.pause))
		if try == rateTries {
			break
		}
		resp.Body.Close()
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, errPrivate) {
			return nil, permanent(fmt.Errorf("%s: %w", site, errPrivate))
		}
		return nil, retryable(fmt.Errorf("%s: %w", site, err))
	}
	defer resp.Body.Close()
	// httpsOnly stops a redirect off https; Go follows the rest, but not 300 or 305.
	if h := resp.Header.Get("Location"); resp.StatusCode/100 == 3 && h != "" {
		if loc, err := req.URL.Parse(h); err == nil && loc.Scheme != "https" {
			return nil, permanent(fmt.Errorf("%s redirected off https to %s", site, loc.Redacted()))
		}
	}
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return nil, gone(fmt.Errorf("Nothing at that address on %s. Check the link", site))
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return nil, retryable(fmt.Errorf("%s answered %s", site, resp.Status))
	case resp.StatusCode != http.StatusOK:
		return nil, permanent(fmt.Errorf("%s answered %s", site, resp.Status))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, retryable(fmt.Errorf("%s: %w", site, err))
	}
	if len(b) > maxBody {
		return nil, permanent(fmt.Errorf("%s sent over %d MiB", site, maxBody>>20))
	}
	return b, nil
}

// getAvatar fetches a card's picture. The card matters more than its
// picture, so a permanent failure logs and returns nil, which keeps the
// avatar card.Decode supplies; a retryable one fails the run.
func (f *fetcher) getAvatar(ctx context.Context, site, url string) ([]byte, error) {
	b, err := f.get(ctx, site, url)
	var perr *protocol.Error
	if err != nil && (!errors.As(err, &perr) || perr.Retryable() || ctx.Err() != nil) {
		return nil, err
	}
	if err != nil {
		log.Printf("%s: no avatar: %v", url, err)
	}
	return b, nil
}

// getJSON fetches url and decodes it into v.
func (f *fetcher) getJSON(ctx context.Context, site, url string, v any) error {
	b, err := f.get(ctx, site, url)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return permanent(fmt.Errorf("%s sent unreadable JSON: %w", site, err))
	}
	return nil
}

func retryable(err error) error {
	return protocol.NewError(protocol.CodeInternalError, err.Error(), true)
}

func permanent(err error) error {
	return protocol.NewError(protocol.CodeInternalError, err.Error(), false)
}

// gone is a page that was deleted, made private, or hides its definition.
func gone(err error) error {
	return protocol.NewError(protocol.CodeRemoteNotFound, err.Error(), false)
}
