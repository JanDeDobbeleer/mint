// Package state encodes and verifies the run state that Mint keeps in one comment on the pull request.
//
// GitHub is the only durable store, so the comment is also an attack surface: anyone who can comment on the PR can
// post text that looks like state. A comment is accepted only when its author matches the trusted identity by numeric
// id and its HMAC verifies. Replays are rejected by the sequence number, and a run key binds a comment to one run.
package state

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/JanDeDobbeleer/mint/internal/core"
)

// MaxPayload is the hard budget for the encoded state, in bytes. Large analysis output belongs in a committed file.
const MaxPayload = 8 << 10

// Version is the wire format version. A comment with another version is rejected, never guessed at.
const Version = 1

var (
	// ErrNoState means the body has no state marker at all.
	ErrNoState = errors.New("state: no state marker")
	// ErrMalformed means the marker is present but the payload cannot be parsed. More than one marker is malformed.
	ErrMalformed = errors.New("state: malformed state comment")
	// ErrTooLarge means the state exceeds MaxPayload.
	ErrTooLarge = errors.New("state: payload exceeds the size budget")
	// ErrBadSignature means no key verifies the payload.
	ErrBadSignature = errors.New("state: signature does not verify")
	// ErrUntrustedAuthor means the comment was not written by Mint's identity.
	ErrUntrustedAuthor = errors.New("state: comment author is not trusted")
	// ErrWrongRun means the signed run key differs from the run being reconciled.
	ErrWrongRun = errors.New("state: comment belongs to another run")
	// ErrReplay means the sequence number is not newer than the one already applied.
	ErrReplay = errors.New("state: sequence number is not newer")
)

// State is the signed run state. Everything in it is visible to anyone who can read the PR, so it never holds a
// secret. Unknown fields are rejected on decode so that a newer writer cannot be silently misread by an older reader.
type State struct {
	// Counters are budget counters keyed by name (attempts, retries, premium requests).
	Counters map[string]int64 `json:"counters,omitempty"`
	// Flow is the flow name.
	Flow string `json:"flow"`
	// Stage is the current stage, or "done".
	Stage string `json:"stage"`
	// Run is the run key (the same value as in the branch name).
	Run string `json:"run"`
	// Head is the head commit SHA the state describes.
	Head string `json:"head,omitempty"`
	// Issue links the trigger, for example "github.com/owner/repo#12".
	Issue string `json:"issue,omitempty"`
	// AgentSession is the Copilot session id Mint chose before launch.
	AgentSession string `json:"agent_session,omitempty"`
	// KID identifies the signing key. Encode sets it; callers do not.
	KID string `json:"kid"`
	// Version is the wire format version. Encode sets it.
	Version int `json:"v"`
	// Attempt counts attempts of the current stage.
	Attempt int `json:"attempt"`
	// Seq increases by one on every write.
	Seq uint64 `json:"seq"`
}

// Comment is the part of a GitHub issue comment the verifier needs.
type Comment struct {
	Body string
	ID   int64
	// AuthorID is the numeric user id, never the login: logins can be renamed and reused.
	AuthorID int64
	// AppID is performed_via_github_app.id, zero when the comment was not made through an App.
	AppID int64
}

// Author is the identity allowed to write state.
type Author struct {
	// UserID is the numeric id of the user (for an App, its bot user).
	UserID int64
	// AppID, when non-zero, must also match performed_via_github_app.id.
	AppID int64
}

// KeyID returns the short identifier of a signing key. It is derived through SHA-256 with a domain prefix, so it
// reveals nothing useful about the key.
func KeyID(key core.Secret) string {
	sum := sha256.Sum256([]byte("mint:state-kid:v1\x00" + key.Reveal()))
	return hex.EncodeToString(sum[:4])
}

const (
	markerOpen  = "<!-- mint:state "
	markerClose = " -->"
)

var markerRE = regexp.MustCompile(`<!-- mint:state ([A-Za-z0-9_-]+)\.([A-Za-z0-9_-]+) -->`)

// Encode renders the comment body: the human text, then the signed marker. human is shown to readers and is not
// trusted by the decoder; it must not contain the marker prefix.
func Encode(s State, key core.Secret, human string) (string, error) {
	if key.IsZero() {
		return "", errors.New("state: signing key is empty")
	}
	if strings.Contains(human, markerOpen) {
		return "", errors.New("state: human text must not contain a state marker")
	}
	s.Version, s.KID = Version, KeyID(key)
	payload, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("state: encode: %w", err)
	}
	if len(payload) > MaxPayload {
		return "", fmt.Errorf("%w: %d bytes, limit %d", ErrTooLarge, len(payload), MaxPayload)
	}
	enc := base64.RawURLEncoding
	body := enc.EncodeToString(payload)
	sig := enc.EncodeToString(sign(key, []byte(body)))
	var b strings.Builder
	if human != "" {
		b.WriteString(strings.TrimRight(human, "\n"))
		b.WriteString("\n\n")
	}
	b.WriteString(markerOpen + body + "." + sig + markerClose)
	return b.String(), nil
}

// Decode extracts and verifies the state in body. keys is every key still accepted (the current one first and any
// previous ones during rotation); the payload's kid selects which to try, and an unknown kid fails. It does not
// check the author: use Verify for comments.
func Decode(body string, keys ...core.Secret) (State, error) {
	matches := markerRE.FindAllStringSubmatch(body, -1)
	if n := strings.Count(body, markerOpen); n == 0 {
		return State{}, ErrNoState
	} else if n != 1 || len(matches) != 1 {
		return State{}, ErrMalformed
	}
	enc := base64.RawURLEncoding
	if enc.DecodedLen(len(matches[0][1])) > MaxPayload {
		return State{}, ErrTooLarge
	}
	payload, err := enc.DecodeString(matches[0][1])
	if err != nil {
		return State{}, ErrMalformed
	}
	sig, err := enc.DecodeString(matches[0][2])
	if err != nil {
		return State{}, ErrMalformed
	}
	var s State
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil || dec.More() {
		return State{}, ErrMalformed
	}
	if s.Version != Version {
		return State{}, fmt.Errorf("%w: unsupported version %d", ErrMalformed, s.Version)
	}
	// The signature covers the encoded text exactly as transmitted, so there is no canonicalisation to get wrong.
	for _, key := range keys {
		if key.IsZero() || KeyID(key) != s.KID {
			continue
		}
		if hmac.Equal(sig, sign(key, []byte(matches[0][1]))) {
			return s, nil
		}
		return State{}, ErrBadSignature
	}
	return State{}, ErrBadSignature
}

// Verify decodes a comment and checks that it is authentic for run: written by the trusted author, correctly signed,
// and bound to the same run key.
func Verify(c Comment, trusted Author, run string, keys ...core.Secret) (State, error) {
	if trusted.UserID == 0 || c.AuthorID != trusted.UserID || (trusted.AppID != 0 && c.AppID != trusted.AppID) {
		return State{}, ErrUntrustedAuthor
	}
	s, err := Decode(c.Body, keys...)
	if err != nil {
		return State{}, err
	}
	if s.Run != run {
		return State{}, ErrWrongRun
	}
	return s, nil
}

// Latest picks the current state from a PR's comments: the authentic comment with the highest sequence number.
// Comments that fail verification are skipped, not fatal, because anyone may post a forged marker; the first error
// seen is returned only when no comment verifies, so callers can tell "no state" from "only forgeries".
func Latest(comments []Comment, trusted Author, run string, keys ...core.Secret) (State, int64, error) {
	var (
		best    State
		bestID  int64
		found   bool
		lastErr = ErrNoState
	)
	for _, c := range comments {
		s, err := Verify(c, trusted, run, keys...)
		if err != nil {
			if !errors.Is(err, ErrNoState) && !errors.Is(err, ErrUntrustedAuthor) {
				lastErr = err
			}
			continue
		}
		if !found || s.Seq > best.Seq {
			best, bestID, found = s, c.ID, true
		}
	}
	if !found {
		return State{}, 0, lastErr
	}
	return best, bestID, nil
}

// Advance returns next with its sequence number set to current's plus one. It is the only way a writer should
// produce the next state, and it refuses a candidate that does not belong to the same run.
func Advance(current, next State) (State, error) {
	if current.Run != next.Run {
		return State{}, ErrWrongRun
	}
	next.Seq = current.Seq + 1
	return next, nil
}

// CheckReplay reports ErrReplay when candidate is not newer than applied. Readers use it before acting on a comment
// that arrives out of order or has been restored from an older edit.
func CheckReplay(applied, candidate State) error {
	if candidate.Seq <= applied.Seq {
		return ErrReplay
	}
	return nil
}

func sign(key core.Secret, data []byte) []byte {
	mac := hmac.New(sha256.New, []byte(key.Reveal()))
	mac.Write(data)
	return mac.Sum(nil)
}
