// Package core holds the domain types shared by Mint's sources, agents and runtimes.
package core

import (
	"fmt"
	"log/slog"
)

const redacted = "[REDACTED]"

// Secret holds a sensitive value. Every formatting, encoding and logging path prints a
// fixed placeholder; the only way to read the value is Reveal. Copies of a Secret share
// the same storage, so Destroy on any copy zeroizes all of them.
//
// The value lives behind a pointer so that reflection-based printing of a struct that
// embeds a Secret in an unexported field shows an address, never the bytes.
type Secret struct{ v *secretValue }

type secretValue struct{ data []byte }

// NewSecret copies value into a new Secret. The caller keeps ownership of value.
func NewSecret(value []byte) Secret {
	return Secret{v: &secretValue{data: append([]byte(nil), value...)}}
}

// NewSecretString is NewSecret for a string.
func NewSecretString(value string) Secret {
	return Secret{v: &secretValue{data: []byte(value)}}
}

// Reveal returns the secret value. Callers must not log it or put it in an error.
func (s Secret) Reveal() string {
	if s.v == nil {
		return ""
	}
	return string(s.v.data)
}

// IsZero reports whether the secret is empty or destroyed.
func (s Secret) IsZero() bool { return s.v == nil || len(s.v.data) == 0 }

// Destroy overwrites the stored value and empties it.
func (s Secret) Destroy() {
	if s.v == nil {
		return
	}
	clear(s.v.data)
	s.v.data = nil
}

func (Secret) String() string   { return redacted }
func (Secret) GoString() string { return redacted }

// Format implements fmt.Formatter so that every verb (%v %+v %#v %s %q %x ...) is redacted.
func (Secret) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(redacted)) }

func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }
func (Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// LogValue implements slog.LogValuer.
func (Secret) LogValue() slog.Value { return slog.StringValue(redacted) }
