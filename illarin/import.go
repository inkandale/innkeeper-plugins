package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/inkandale/innkeeper-plugins/card"
	"github.com/inkandale/innkeeper-plugins/internal/httpkit"
	"github.com/inkandale/innkeeper-plugins/internal/pluginio"
	"github.com/inkandale/innkeeper/protocol"
)

// params are an import's settings: URL, a work's page, or Sent, every work sent to Innkeeper from Illarin.
type params struct {
	URL  string `json:"url,omitempty"`
	Sent bool   `json:"sent,omitempty"`
}

type notifyFunc func(method string, params any) error

var workPath = regexp.MustCompile(`^/a/([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})(/[^/]*)?/?$`)

// maxEmptyWaits is how many send waits a URL import sits through before
// giving up on its work; each wait is 25 to 30 seconds.
const maxEmptyWaits = 3

// send is one work Illarin released to this installation.
type send struct {
	ID     string `json:"id"`
	WorkID string `json:"workId"`
	Type   string `json:"type"`
	Name   string `json:"name"`
	Files  []struct {
		Type    string `json:"type"`
		URL     string `json:"url"`
		IsCover bool   `json:"isCover"`
	} `json:"files"`
}

// run answers import.run.
func (in *instance) run(ctx context.Context, notify notifyFunc, raw json.RawMessage) (any, error) {
	var p protocol.ImportRunParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	var ps params
	dec := json.NewDecoder(bytes.NewReader(p.Params))
	dec.DisallowUnknownFields()
	err := dec.Decode(&ps)
	if err == nil && (strings.TrimSpace(ps.URL) == "") == !ps.Sent {
		err = errors.New("set url or sent, not both")
	}
	if err != nil {
		return nil, protocol.InvalidParams(fmt.Errorf(`params must be {"url": "<work page URL>"} or {"sent": true}: %w`, err))
	}
	if in.err != nil {
		return nil, protocol.Fail(in.err.Error(), false)
	}
	c := in.client
	var work string
	if !ps.Sent {
		if work, err = c.workID(ps.URL); err != nil {
			return nil, protocol.InvalidParams(err)
		}
	}
	say := func(msg string) error {
		if err := notify(protocol.NotifyLog, protocol.Log{Run: p.Run, Message: msg}); err != nil {
			return err
		}
		return notify(protocol.NotifyProgress, protocol.Progress{Run: p.Run, Message: msg})
	}
	if _, err := c.token(ctx, false); errors.Is(err, errNotConnected) {
		if err := c.connect(ctx, say); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	var want string
	if work != "" {
		var err error
		if want, err = c.receive(ctx, work); err != nil {
			return nil, err
		}
	}
	r := &collection{client: c, blobTmp: in.blobTmp, run: p.Run, notify: notify}
	if err := r.collect(ctx, want); err != nil {
		return nil, err
	}
	return struct{}{}, nil
}

// workID reads a work's page URL on c's server, {base}/a/{id}/{slug}.
func (c *client) workID(page string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(page))
	base, _ := url.Parse(c.base)
	if err != nil || !strings.EqualFold(u.Host, base.Host) {
		return "", fmt.Errorf("%q is not a page on %s", page, c.base)
	}
	m := workPath.FindStringSubmatch(u.Path)
	if m == nil {
		return "", fmt.Errorf("an Illarin work URL looks like %s/a/{id}/{name}", c.base)
	}
	return strings.ToLower(m[1]), nil
}

// receive asks Illarin to send work's latest version to this installation and returns the send's id.
func (c *client) receive(ctx context.Context, work string) (string, error) {
	resp, err := c.authed(ctx, http.MethodPost, "/api/v1/works/"+work+"/receive", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		return "", protocol.Fail("Illarin refused to send the work: it is private, or receiving works is off for Innkeeper in Illarin's account settings", false)
	}
	var queued struct {
		ID string `json:"id"`
	}
	err = httpkit.Decode(resp, "Illarin receive", &queued)
	return queued.ID, err
}

// collection is one run's pass over the send queue.
type collection struct {
	client  *client
	blobTmp string
	run     string
	notify  notifyFunc
	done    int64
}

// collect imports and acknowledges what Illarin releases until the queue is
// empty and, if want is set, that send has arrived.
func (r *collection) collect(ctx context.Context, want string) error {
	ack := []string{}
	empty := 0
	seen := want == ""
	for {
		sends, err := r.wait(ctx, ack)
		if err != nil {
			return err
		}
		ack = []string{}
		if len(sends) == 0 {
			if seen {
				return nil
			}
			if empty++; empty >= maxEmptyWaits {
				return protocol.Fail("Illarin hasn't released the work yet; try again later", true)
			}
			continue
		}
		for _, s := range sends {
			err := r.emit(ctx, s)
			var perr *protocol.Error
			if errors.As(err, &perr) && perr.Retryable() || ctx.Err() != nil {
				return err
			}
			if s.ID == want {
				seen = true
				if err != nil {
					return err
				}
			} else if err != nil {
				// A broken send won't mend; acknowledging it stops Illarin offering it again.
				log.Printf("send %s: %v", s.ID, err)
				_ = r.notify(protocol.NotifyLog, protocol.Log{Run: r.run, Level: "warn", Message: fmt.Sprintf("skipped %s: %v", s.Name, err)})
			}
			ack = append(ack, s.ID)
		}
	}
}

// wait is one send wait: it acknowledges ack and returns what Illarin releases, or nothing after 25 to 30 seconds.
func (r *collection) wait(ctx context.Context, ack []string) ([]send, error) {
	resp, err := r.client.authed(ctx, http.MethodPost, "/api/v1/sends/collect", map[string][]string{"acknowledge": ack})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode == http.StatusForbidden {
		return nil, protocol.Fail("receiving works is off for Innkeeper in Illarin's account settings", false)
	}
	var got struct {
		Sends []send `json:"sends"`
	}
	err = httpkit.Decode(resp, "Illarin collect", &got)
	return got.Sends, err
}

// emit fetches s's export and sends it to the core as a card or lorebook.
// ponytail: emit.* has no reply, so the next wait acknowledges before the core
// has stored the card; a lost one comes back by importing its URL again.
func (r *collection) emit(ctx context.Context, s send) error {
	var export, cover string
	for _, f := range s.Files {
		switch {
		case f.Type == "export":
			export = f.URL
		case f.Type == "picture" && f.IsCover:
			cover = f.URL
		}
	}
	if export == "" {
		return protocol.Fail(fmt.Sprintf("Illarin sent %s without an export", s.Name), false)
	}
	b, err := r.client.file(ctx, export)
	if err != nil {
		return err
	}
	item := r.client.base + "/a/" + s.WorkID
	if s.Type == "lorebook" {
		book, err := card.DecodeLorebook(b, s.Name)
		if err != nil {
			return protocol.Fail(fmt.Sprintf("%s: %v", item, err), false)
		}
		err = r.notify(protocol.NotifyEmitLorebook, protocol.EmitLorebook{Run: r.run, Item: item, Lorebook: book})
		return r.progress(err)
	}
	c, img, err := card.Decode(b)
	if err != nil {
		return protocol.Fail(fmt.Sprintf("%s: %v", item, err), false)
	}
	if img == nil && cover != "" {
		img, err = r.cover(ctx, cover)
		if err != nil {
			return err
		}
	}
	ev := protocol.EmitCard{Run: r.run, Item: item, Card: c}
	if img != nil {
		if ev.Avatar, err = pluginio.WriteTmp(r.blobTmp, "avatar-*.png", img); err != nil {
			return err
		}
	}
	return r.progress(r.notify(protocol.NotifyEmitCard, ev))
}

// cover fetches a picture as a PNG. The card matters more, so only a retryable failure fails it.
func (r *collection) cover(ctx context.Context, u string) ([]byte, error) {
	b, err := r.client.file(ctx, u)
	if err == nil {
		b, err = card.AsPNG(b)
	}
	var perr *protocol.Error
	if errors.As(err, &perr) && perr.Retryable() {
		return nil, err
	}
	if err != nil {
		log.Printf("%s: no avatar: %v", u, err)
		return nil, nil
	}
	return b, nil
}

func (r *collection) progress(err error) error {
	if err != nil {
		return err
	}
	r.done++
	return r.notify(protocol.NotifyProgress, protocol.Progress{Run: r.run, Done: r.done})
}

// file fetches one of a send's signed addresses, which carry their own authorization.
func (c *client) file(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil || req.URL.Scheme != "https" {
		return nil, protocol.Fail(fmt.Sprintf("Illarin sent a file address that isn't https: %q", u), false)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, protocol.Fail(fmt.Sprintf("Illarin file: %v", err), true)
	}
	defer resp.Body.Close()
	var b []byte
	err = httpkit.Decode(resp, "Illarin file", &b)
	return b, err
}
