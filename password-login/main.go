// Command password-login is a login provider that checks one password and
// reports the subject "owner". Run "password-login hash" to make the
// password_hash secret from a password on stdin.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/inkandale/innkeeper/protocol"
	"golang.org/x/crypto/bcrypt"
)

// subject is who every login is. The owner sets it as the instance's owner_subject.
const subject = "owner"

// cost is the bcrypt cost for new hashes: about a quarter second each.
const cost = 12

const form = `{"type": "object", "properties": {"password": {"type": "string", "format": "password", "title": "Password"}}, "required": ["password"]}`

// minPassword is the shortest password setup accepts.
const minPassword = 12

const setupForm = `{"type": "object", "properties": {"password": {"type": "string", "format": "password", "title": "Password"}, "confirm": {"type": "string", "format": "password", "title": "Confirm password"}}, "required": ["password", "confirm"]}`

func main() {
	if len(os.Args) == 2 && os.Args[1] == "hash" {
		if err := printHash(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	protocol.Serve(protocol.Plugin[string]{
		Init: func(_ context.Context, p protocol.InitializeParams) (*string, error) {
			h := p.Secrets["password_hash"]
			return &h, nil
		},
		Methods: map[string]func(*string, context.Context, json.RawMessage) (any, error){
			protocol.MethodAuthBegin: func(*string, context.Context, json.RawMessage) (any, error) {
				return protocol.AuthBeginResult{Form: json.RawMessage(form)}, nil
			},
			protocol.MethodAuthComplete: func(hash *string, _ context.Context, params json.RawMessage) (any, error) {
				return complete(*hash, params)
			},
			protocol.MethodAuthSetup: func(*string, context.Context, json.RawMessage) (any, error) {
				return protocol.AuthSetupResult{Form: json.RawMessage(setupForm)}, nil
			},
			protocol.MethodAuthSetupComplete: func(_ *string, _ context.Context, params json.RawMessage) (any, error) {
				return setupComplete(params)
			},
		},
	})
}

// complete checks the form's password against hash.
func complete(hash string, params json.RawMessage) (any, error) {
	var p protocol.AuthCompleteParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	if hash == "" {
		return nil, protocol.Fail("the instance has no password_hash secret; make one with: password-login hash", false)
	}
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(p.Params["password"]))
	switch {
	case err == nil:
		return protocol.AuthCompleteResult{Subject: subject, Name: "Owner"}, nil
	case errors.Is(err, bcrypt.ErrMismatchedHashAndPassword):
		return nil, protocol.Fail("wrong password", false)
	}
	return nil, protocol.Fail("password_hash is not a bcrypt hash: "+err.Error(), false)
}

// setupComplete checks a new password and returns its hash as the secret.
func setupComplete(params json.RawMessage) (any, error) {
	var p protocol.AuthSetupCompleteParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	pw := p.Params["password"]
	switch {
	case pw != p.Params["confirm"]:
		return nil, protocol.Fail("the passwords don't match", false)
	case len([]rune(pw)) < minPassword:
		return nil, protocol.Fail(fmt.Sprintf("use at least %d characters", minPassword), false)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), cost)
	if err != nil {
		return nil, protocol.Fail(err.Error(), false)
	}
	return protocol.AuthSetupCompleteResult{Secrets: map[string]string{"password_hash": string(h)}, Subject: subject}, nil
}

// printHash reads a password line from r and writes its bcrypt hash to w.
func printHash(r io.Reader, w io.Writer) error {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && err != io.EOF {
		return err
	}
	password := strings.TrimRight(line, "\r\n")
	if password == "" {
		return errors.New("no password on stdin")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(h))
	return err
}
