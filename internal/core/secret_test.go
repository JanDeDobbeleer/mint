package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const marker = "super-secret-value"

type holder struct {
	Inner  Secret
	hidden Secret
	Public string
}

func TestSecretNeverAppearsThroughFormattingVerbs(t *testing.T) {
	s := NewSecretString(marker)
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%T", "%10s"} {
		assert.NotContains(t, fmt.Sprintf(verb, s), marker, verb)
		assert.NotContains(t, fmt.Sprintf(verb, &s), marker, verb)
		assert.NotContains(t, fmt.Sprintf(verb, []Secret{s}), marker, verb)
		assert.NotContains(t, fmt.Sprintf(verb, map[string]Secret{"k": s}), marker, verb)
	}
	assert.Equal(t, redacted, s.String())
	assert.Equal(t, redacted, s.GoString())
	assert.Equal(t, redacted, fmt.Sprint(s))
}

func TestSecretInsideStructsStaysRedacted(t *testing.T) {
	h := holder{Public: "x", Inner: NewSecretString(marker), hidden: NewSecretString(marker)}
	for _, verb := range []string{"%v", "%+v", "%#v"} {
		assert.NotContains(t, fmt.Sprintf(verb, h), marker, verb)
		assert.NotContains(t, fmt.Sprintf(verb, &h), marker, verb)
	}
}

func TestSecretNeverAppearsInJSONOrText(t *testing.T) {
	s := NewSecretString(marker)
	data, err := json.Marshal(map[string]any{"s": s, "h": holder{Inner: s}, "p": &s})
	require.NoError(t, err)
	assert.NotContains(t, string(data), marker)
	assert.Contains(t, string(data), redacted)
	text, err := s.MarshalText()
	require.NoError(t, err)
	assert.NotContains(t, string(text), marker)
}

func TestSecretNeverAppearsInSlog(t *testing.T) {
	s := NewSecretString(marker)
	for _, handler := range []func(*bytes.Buffer) slog.Handler{
		func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
		func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
	} {
		var out bytes.Buffer
		logger := slog.New(handler(&out))
		logger.Info("event", "secret", s, slog.Any("holder", holder{Inner: s}), slog.Group("g", slog.Any("s", s)))
		assert.NotContains(t, out.String(), marker)
		assert.Contains(t, out.String(), redacted)
	}
}

func TestSecretNeverAppearsThroughErrorWrapping(t *testing.T) {
	s := NewSecretString(marker)
	err := fmt.Errorf("launch failed with %v: %w", s, errors.New("boom"))
	wrapped := fmt.Errorf("outer %s %q %+v: %w", s, s, s, err)
	assert.NotContains(t, wrapped.Error(), marker)
	assert.NotContains(t, fmt.Sprintf("%#v", wrapped), marker)
}

func TestSecretRevealAndDestroy(t *testing.T) {
	source := []byte(marker)
	s := NewSecret(source)
	copyOfSecret := s
	clear(source)
	assert.Equal(t, marker, s.Reveal())
	assert.False(t, s.IsZero())
	s.Destroy()
	assert.Empty(t, copyOfSecret.Reveal(), "copies share storage, so destroying one destroys all")
	assert.True(t, copyOfSecret.IsZero())

	var zero Secret
	assert.Empty(t, zero.Reveal())
	assert.True(t, zero.IsZero())
	zero.Destroy()
	assert.Equal(t, redacted, fmt.Sprint(zero))
}

func TestSecretDestroyZeroizesBackingBytes(t *testing.T) {
	s := NewSecretString(marker)
	backing := s.v.data
	s.Destroy()
	assert.Equal(t, make([]byte, len(marker)), backing)
}
