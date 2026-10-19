package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sync"
	"syscall"

	"github.com/inkandale/innkeeper-plugins/internal/pluginio"
	"github.com/inkandale/innkeeper/protocol"
)

// checkpointEvery is how many rows the import reads between checkpoints.
// Tests set it to 1.
var checkpointEvery = 1000

// imageReaders is how many versions' images the import reads ahead of the
// row it is sending, since each read waits on the archive's share.
const imageReaders = 16

// params are an import's settings: Dump, the path of the archive's SQL dump.
type params struct {
	Dump string `json:"dump"`
}

// pending is a row read from the dump, with its avatar once done is closed.
type pending struct {
	table     string
	row       map[string]string
	at        checkpoint // where the dump is after this row
	err       error      // from reading the row
	avatar    string     // a file in blob_tmp, or ""
	avatarLog string     // why the version has no image, for the job's log
	avatarErr error      // fails the run
	done      chan struct{}
}

// dropAvatar deletes the row's avatar file, for a row the core won't take.
func (r *pending) dropAvatar() {
	if r.avatar != "" {
		os.Remove(r.avatar)
	}
}

// notifyFunc sends a notification to the core.
type notifyFunc func(method string, params any) error

// run answers import.run: it reads the dump from the checkpoint to the end
// and sends each card and lorebook version in dump order.
func run(ctx context.Context, e *env, notify notifyFunc, raw json.RawMessage) (any, error) {
	var p protocol.ImportRunParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	var ps params
	dec := json.NewDecoder(bytes.NewReader(p.Params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ps); err != nil || ps.Dump == "" {
		return nil, protocol.InvalidParams(fmt.Errorf(`params must be {"dump": "<path of the SQL dump>"}: %v`, err))
	}
	var at checkpoint
	if len(p.Checkpoint) > 0 && string(p.Checkpoint) != "null" {
		if err := json.Unmarshal(p.Checkpoint, &at); err != nil {
			return nil, protocol.InvalidParams(fmt.Errorf("checkpoint: %w", err))
		}
	}
	for _, dir := range e.images {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return nil, protocol.InvalidParams(fmt.Errorf("config images: %s is not a folder; is the archive mounted?", dir))
		}
	}
	d, err := openDump(ps.Dump, at)
	if errors.Is(err, errDumpChanged) {
		return nil, protocol.InvalidParams(err)
	}
	if err != nil {
		return nil, protocol.InvalidParams(fmt.Errorf("params dump: %w", err))
	}
	defer d.Close()
	parents := map[string]parent{}
	// Only chub's versions read the per-item rows, and they follow the block directly.
	if at.Parents != 0 && (at.Table == parentTable || at.Table == parentVersions) {
		if parents, err = readParents(ctx, ps.Dump, at); err != nil {
			return nil, retryable(err)
		}
	}
	report := func(at checkpoint) error {
		if err := notify(protocol.NotifyProgress, protocol.Progress{Run: p.Run, Done: at.Offset, Total: at.Size}); err != nil {
			return err
		}
		cp, _ := json.Marshal(at)
		return notify(protocol.NotifyCheckpoint, protocol.Checkpoint{Run: p.Run, Checkpoint: cp})
	}
	// One goroutine reads the dump and starts each version's image read, and
	// this one sends rows in dump order, so a checkpoint never passes a row
	// still in flight.
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	rows := make(chan *pending, imageReaders)
	defer func() {
		cancel()
		wg.Wait()
		// Rows read ahead but never sent leave their avatars behind otherwise.
		for r := range rows {
			r.dropAvatar()
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(rows)
		for {
			table, row, err := d.next(ctx)
			if errors.Is(err, io.EOF) {
				return
			}
			r := &pending{table: table, row: row, at: d.at, err: err, done: make(chan struct{})}
			if err == nil && table != parentTable && sources[table].kind == protocol.KindCard {
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer close(r.done)
					r.avatar, r.avatarLog, r.avatarErr = loadAvatar(e, r.row)
				}()
			} else {
				close(r.done)
			}
			select {
			case rows <- r:
			case <-ctx.Done():
				<-r.done
				r.dropAvatar()
				return
			}
			if err != nil && !errors.Is(err, errBadRow) {
				return
			}
		}
	}()
	n := 0
	for r := range rows {
		<-r.done
		if err := ctx.Err(); err != nil {
			r.dropAvatar()
			return nil, err
		}
		n++
		err := r.err
		if err == nil {
			err = r.avatarErr
		}
		if err == nil && r.avatarLog != "" {
			err = logLine(notify, p.Run, r.avatarLog)
		}
		if err == nil {
			err = sendRow(p.Run, r, parents, notify)
		}
		if errors.Is(err, errBadRow) {
			r.dropAvatar()
			err = logLine(notify, p.Run, fmt.Sprintf("skipped %s %s increment %s, ending at byte %d: %v",
				r.table, r.row[idColumn(r.table)], r.row["increment"], r.at.Offset, err))
		}
		if err != nil {
			// A failed send leaves the file with the plugin, not the core.
			r.dropAvatar()
			return nil, retryable(err)
		}
		if n%checkpointEvery == 0 {
			if err := report(r.at); err != nil {
				return nil, err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// The reader is done, so d.at is where the dump ended.
	return struct{}{}, report(d.at)
}

// retryable marks an error the plugin can't fix itself, such as the
// archive's disk failing, so the core retries from the last checkpoint.
func retryable(err error) error {
	var rpc *protocol.Error
	if errors.As(err, &rpc) || errors.Is(err, context.Canceled) {
		return err
	}
	return protocol.NewError(protocol.CodeInternalError, err.Error(), true)
}

// idColumn names the column holding a row's item ID.
func idColumn(table string) string {
	if src, ok := sources[table]; ok {
		return src.idCol
	}
	return "id"
}

// sendRow keeps a chub per-item row for later versions, or sends a version.
func sendRow(run string, r *pending, parents map[string]parent, notify notifyFunc) error {
	if r.table == parentTable {
		id, p, err := readParent(r.row)
		if err == nil {
			parents[id] = p
		}
		return err
	}
	v, err := readVersion(r.table, r.row, parents)
	if err != nil {
		return err
	}
	if v.kind == protocol.KindLorebook {
		return notify(protocol.NotifyEmitLorebook, protocol.EmitLorebook{Run: run, Item: v.item, Lorebook: v.data})
	}
	return notify(protocol.NotifyEmitCard, protocol.EmitCard{Run: run, Item: v.item, Card: v.data, Avatar: r.avatar,
		Version: v.label, Created: v.created, Values: v.values})
}

// readParents reads chub's per-item block again for a run resumed past its
// start, since later versions take their values from it.
func readParents(ctx context.Context, path string, at checkpoint) (map[string]parent, error) {
	d, err := openDump(path, checkpoint{Offset: at.Parents, Size: at.Size})
	if err != nil {
		return nil, err
	}
	defer d.Close()
	parents := map[string]parent{}
	for {
		table, row, err := d.next(ctx)
		switch {
		case errors.Is(err, io.EOF) || (table != parentTable && err == nil):
			return parents, nil
		case errors.Is(err, errBadRow):
			continue
		case err != nil:
			return nil, err
		}
		if id, p, err := readParent(row); err == nil {
			parents[id] = p
		}
	}
}

// logLine adds a warning to the log of the job the run belongs to.
func logLine(notify notifyFunc, run, message string) error {
	return notify(protocol.NotifyLog, protocol.Log{Run: run, Level: "warn", Message: message})
}

// loadAvatar copies a card version's image from the archive into blob_tmp.
// An image that can never be read is left out, with why for the job's log;
// any other read or write error is returned, to fail the run for the core to
// retry.
func loadAvatar(e *env, row map[string]string) (path, problem string, err error) {
	hash := row["image_hash"]
	b, err := readImage(e.images, hash)
	if err != nil && !errors.Is(err, fs.ErrPermission) && !errors.Is(err, syscall.EISDIR) && !errors.Is(err, pluginio.ErrTooLarge) {
		return "", "", err
	}
	if err == nil && b != nil && imageExt(b) == "" {
		err = errors.New("not a PNG, WebP, JPEG, or GIF")
	}
	if err != nil {
		return "", fmt.Sprintf("image %s: %v", hash, err), nil
	}
	if b == nil {
		return "", "", nil
	}
	path, err = pluginio.WriteTmp(e.blobTmp, "avatar-*."+imageExt(b), b)
	return path, "", err
}
