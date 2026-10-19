package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/inkandale/innkeeper/protocol"
)

// skillType is the one record type the manifest declares.
const skillType = "skill"

var errNoDatabase = errors.New("no database: the core sends database_url to a plugin whose manifest sets database = true")

// records answers the records.* methods.
func (srv *server) records(ctx context.Context, in *instance, method string, params json.RawMessage) (any, error) {
	var p struct {
		Type   string          `json:"type"`
		ID     string          `json:"id"`
		Record json.RawMessage `json:"record"`
		URL    string          `json:"url"`
		Path   string          `json:"path"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	if p.Type != skillType {
		return nil, protocol.InvalidParams(fmt.Errorf("unknown record type %q", p.Type))
	}
	if in.store == nil {
		return nil, errNoDatabase
	}
	switch method {
	case protocol.MethodRecordsList:
		skills, err := in.store.summaries(ctx, false)
		if err != nil {
			return nil, err
		}
		res := protocol.RecordsListResult{Records: []protocol.RecordSummary{}}
		for _, sk := range skills {
			res.Records = append(res.Records, summary(sk))
		}
		return res, nil
	case protocol.MethodRecordsGet:
		sk, err := in.store.get(ctx, p.ID)
		if err != nil {
			return nil, recordError(p.ID, err)
		}
		b, err := json.Marshal(sk)
		return protocol.RecordsGetResult{Record: b}, err
	case protocol.MethodRecordsPut:
		sk := skill{Enabled: true}
		if err := json.Unmarshal(p.Record, &sk); err != nil {
			return nil, protocol.InvalidParams(fmt.Errorf("record: %v", err))
		}
		if err := sk.check(); err != nil {
			return nil, err
		}
		var err error
		if p.ID == "" {
			err = in.store.create(ctx, sk)
		} else {
			err = in.store.replace(ctx, p.ID, sk)
		}
		if err != nil {
			return nil, recordError(p.ID, err)
		}
		return protocol.RecordsPutResult{ID: sk.Name}, nil
	case protocol.MethodRecordsDelete:
		if err := in.store.delete(ctx, p.ID); err != nil {
			return nil, recordError(p.ID, err)
		}
		return struct{}{}, nil
	}
	sk, err := importSkill(ctx, p.URL, p.Path)
	if err != nil {
		return nil, err
	}
	if err := in.store.upsert(ctx, sk); err != nil {
		return nil, err
	}
	return protocol.RecordsImportResult{Records: []protocol.RecordSummary{summary(sk)}}, nil
}

// importSkill reads a skill from url, or from the uploaded file at path. The
// upload's name isn't used: the content says whether it's a zip.
func importSkill(ctx context.Context, url, path string) (skill, error) {
	if url != "" {
		return fetchSkill(ctx, url)
	}
	if path == "" {
		return skill{}, protocol.InvalidParams(errors.New("import needs a url or a path"))
	}
	f, err := os.Open(path)
	if err != nil {
		return skill{}, err
	}
	defer f.Close()
	b, err := readUpload(f)
	if err != nil {
		return skill{}, err
	}
	return parseUpload(b)
}

func summary(sk skill) protocol.RecordSummary {
	return protocol.RecordSummary{ID: sk.Name, Name: sk.Name, Description: sk.Description}
}

func recordError(id string, err error) error {
	switch {
	case errors.Is(err, errSkillNotFound):
		return protocol.NewError(protocol.CodeRemoteNotFound, "no skill "+id, false)
	case errors.Is(err, errSkillExists):
		return protocol.InvalidParams(errors.New("a skill with that name exists; rename this one or edit the existing skill"))
	}
	return err
}
