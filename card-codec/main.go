// Command card-codec is a codec plugin for character cards: CCv3, V2, and V1
// as PNG and JSON. It also reads CharX.
package main

import (
	"context"
	"encoding/json"

	"github.com/inkandale/innkeeper-plugins/card"
	"github.com/inkandale/innkeeper-plugins/internal/pluginio"
	"github.com/inkandale/innkeeper/protocol"
)

func main() {
	protocol.Serve(protocol.Plugin[string]{
		Init: func(_ context.Context, p protocol.InitializeParams) (*string, error) { return &p.BlobTmp, nil },
		Methods: map[string]func(*string, context.Context, json.RawMessage) (any, error){
			protocol.MethodCodecDecode: func(tmp *string, _ context.Context, params json.RawMessage) (any, error) {
				return decode(*tmp, params)
			},
			protocol.MethodCodecEncode: func(tmp *string, _ context.Context, params json.RawMessage) (any, error) {
				return encode(*tmp, params)
			},
		},
	})
}

func decode(tmp string, params json.RawMessage) (any, error) {
	var p protocol.CodecDecodeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	b, err := pluginio.ReadFile(p.Path)
	if err != nil {
		return nil, protocol.InvalidParams(err)
	}
	c, img, err := card.Decode(b)
	if err != nil {
		return nil, protocol.InvalidParams(err)
	}
	res := protocol.CodecDecodeResult{Card: c}
	if img != nil {
		if res.Avatar, err = pluginio.WriteTmp(tmp, "avatar-*.png", img); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func encode(tmp string, params json.RawMessage) (any, error) {
	var p protocol.CodecEncodeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	var avatar []byte
	if p.Avatar != "" {
		var err error
		if avatar, err = pluginio.ReadFile(p.Avatar); err != nil {
			return nil, err
		}
	}
	out, ext, err := card.Encode(p.Card, p.Format, avatar)
	if err != nil {
		return nil, protocol.InvalidParams(err)
	}
	path, err := pluginio.WriteTmp(tmp, "card-*."+ext, out)
	if err != nil {
		return nil, err
	}
	return protocol.CodecEncodeResult{Path: path, Extension: ext}, nil
}
