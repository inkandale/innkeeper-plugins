package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/inkandale/innkeeper-plugins/card"
	"github.com/inkandale/innkeeper-plugins/internal/pluginio"
	"github.com/inkandale/innkeeper/protocol"
)

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// batch is how many files a folder import reads between checkpoints. The
// core writes each checkpoint to the database.
const batch = 20

// env is what initialize sends that imports need.
type env struct {
	folder  string // config.folder: the only folder imports may read
	blobTmp string
	library string // the library blob store, which holds uploads
}

// params are an import's settings: Path, a folder inside config.folder, or
// Blob, the sha256 of an uploaded file, with Name, its original file name.
type params struct {
	Path string `json:"path"`
	Blob string `json:"blob"`
	Name string `json:"name"`
}

// checkpoint is the last file a folder import finished.
type checkpoint struct {
	Last string `json:"last"`
}

// notifyFunc sends a notification to the core.
type notifyFunc func(method string, params any) error

// importer runs one import.run call.
type importer struct {
	env
	run    string
	notify notifyFunc
}

// run answers import.run.
func run(ctx context.Context, e env, notify notifyFunc, raw json.RawMessage) (any, error) {
	var p protocol.ImportRunParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	var ps params
	if len(p.Params) > 0 && string(p.Params) != "null" {
		dec := json.NewDecoder(bytes.NewReader(p.Params))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&ps); err != nil {
			return nil, protocol.InvalidParams(fmt.Errorf("params: %w", err))
		}
	}
	var cp checkpoint
	if len(p.Checkpoint) > 0 && string(p.Checkpoint) != "null" {
		if err := json.Unmarshal(p.Checkpoint, &cp); err != nil {
			return nil, protocol.InvalidParams(fmt.Errorf("checkpoint: %w", err))
		}
	}
	im := &importer{env: e, run: p.Run, notify: notify}
	var err error
	switch {
	case ps.Blob != "" && ps.Path != "":
		err = protocol.InvalidParams(errors.New("params: give path or blob, not both"))
	case ps.Blob != "":
		err = im.importUpload(ps.Blob, ps.Name)
	default:
		err = im.importFolder(ctx, path.Clean(ps.Path), cp.Last)
	}
	if err != nil {
		return nil, err
	}
	return struct{}{}, nil
}

// importUpload imports one uploaded file from the library blob store.
func (im *importer) importUpload(sha, name string) error {
	if !sha256Pattern.MatchString(sha) {
		return protocol.InvalidParams(errors.New("params: blob must be a sha256 in lowercase hex"))
	}
	if im.library == "" {
		return errors.New("initialize sent no library blob store")
	}
	b, err := pluginio.ReadFile(filepath.Join(im.library, sha[:2], sha[2:4], sha))
	if errors.Is(err, fs.ErrNotExist) {
		return protocol.InvalidParams(fmt.Errorf("no uploaded file %s; uploads are kept for 24 hours", sha))
	}
	if err != nil {
		return err
	}
	if err := im.emit(sha, name, b); errors.Is(err, card.ErrNotCard) {
		return protocol.InvalidParams(fmt.Errorf("%s is not a card or lorebook: %w", cmp.Or(name, sha), err))
	} else if err != nil {
		return err
	}
	return im.notify(protocol.NotifyProgress, protocol.Progress{Run: im.run, Done: 1, Total: 1})
}

// importFolder imports every .png, .json, and .charx file under dir, a slash path
// inside config.folder, resuming after the file last.
func (im *importer) importFolder(ctx context.Context, dir, last string) error {
	if im.folder == "" {
		return protocol.InvalidParams(errors.New("this instance has no folder in its config; set one, or import an upload with params.blob"))
	}
	if !fs.ValidPath(dir) {
		return protocol.InvalidParams(fmt.Errorf("params: path %q must be relative to the folder and stay inside it", dir))
	}
	root, err := os.OpenRoot(im.folder)
	if err != nil {
		return protocol.InvalidParams(fmt.Errorf("config folder: %w", err))
	}
	defer root.Close()

	// Walk first, for a progress total and a fixed order to resume in.
	var files []string
	err = fs.WalkDir(root.FS(), dir, func(name string, d fs.DirEntry, err error) error {
		if err != nil && name != dir {
			// One unreadable subfolder shouldn't stop the rest of the import.
			log.Printf("skipped %s: %v", name, err)
			return nil
		}
		if err != nil {
			return err
		}
		ext := strings.ToLower(path.Ext(name))
		if d.Type().IsRegular() && (ext == ".png" || ext == ".json" || ext == ".charx") {
			files = append(files, name)
		}
		return nil
	})
	if err != nil {
		return protocol.InvalidParams(err)
	}
	// ponytail: if last is gone, the import starts over; the core skips items it has.
	start := slices.Index(files, last) + 1

	skipped := 0
	for i := start; i < len(files); i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := files[i]
		b, err := pluginio.ReadIn(root, name)
		if err == nil {
			if err = im.emit(name, name, b); err != nil && !errors.Is(err, card.ErrNotCard) {
				return err
			}
		}
		if err != nil {
			skipped++
			log.Printf("skipped %s: %v", name, err)
		}
		if n := i + 1; n%batch == 0 || n == len(files) {
			p := protocol.Progress{Run: im.run, Done: int64(n), Total: int64(len(files))}
			if skipped > 0 {
				p.Message = fmt.Sprintf("skipped %d file(s) that aren't cards or lorebooks, or can't be read", skipped)
			}
			if err := im.notify(protocol.NotifyProgress, p); err != nil {
				return err
			}
			cp, _ := json.Marshal(checkpoint{Last: name})
			if err := im.notify(protocol.NotifyCheckpoint, protocol.Checkpoint{Run: im.run, Checkpoint: cp}); err != nil {
				return err
			}
		}
	}
	return nil
}

// emit decodes one file and sends the card or lorebook it holds. A file that
// is neither gets an error wrapping card.ErrNotCard. name, the file's name,
// names a lorebook that has none.
func (im *importer) emit(item, name string, b []byte) error {
	stem := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	// Lorebooks first: one with a name and description would pass as a V1 card.
	if book, err := card.DecodeLorebook(b, stem); err == nil {
		return im.notify(protocol.NotifyEmitLorebook, protocol.EmitLorebook{Run: im.run, Item: item, Lorebook: book})
	}
	c, img, err := card.Decode(b)
	if err != nil {
		return err
	}
	ev := protocol.EmitCard{Run: im.run, Item: item, Card: c}
	if img != nil {
		if ev.Avatar, err = pluginio.WriteTmp(im.blobTmp, "avatar-*.png", img); err != nil {
			return err
		}
	}
	return im.notify(protocol.NotifyEmitCard, ev)
}
