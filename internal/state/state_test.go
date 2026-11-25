package state

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/JanDeDobbeleer/mint/internal/core"
)

var (
	keyA    = core.NewSecretString("0123456789abcdef0123456789abcdef-A")
	keyB    = core.NewSecretString("fedcba9876543210fedcba9876543210-B")
	trusted = Author{UserID: 4242, AppID: 77}
)

func sample() State {
	return State{
		Flow: "issue-to-pr", Stage: "implement", Run: "acme-api-12", Head: "abc123", Issue: "github.com/me/acme-api#12",
		AgentSession: "43babbf7-c4f7-45e7-94f4-0fd444d6b156", Attempt: 2, Seq: 5,
		Counters: map[string]int64{"premium": 3},
	}
}

func comment(body string) Comment {
	return Comment{ID: 1, AuthorID: trusted.UserID, AppID: trusted.AppID, Body: body}
}

func TestRoundTrip(t *testing.T) {
	body, err := Encode(sample(), keyA, "Mint is working on this.")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(body, "Mint is working on this.\n\n<!-- mint:state "))

	got, err := Decode(body, keyA)
	require.NoError(t, err)
	want := sample()
	want.Version, want.KID = Version, KeyID(keyA)
	assert.Equal(t, want, got)
}

func TestEncodeNeverContainsTheKey(t *testing.T) {
	body, err := Encode(sample(), keyA, "")
	require.NoError(t, err)
	assert.NotContains(t, body, keyA.Reveal())
	assert.NotContains(t, KeyID(keyA), keyA.Reveal())

	_, err = Decode(body, keyB)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), keyA.Reveal())
	assert.NotContains(t, err.Error(), keyB.Reveal())
}

func TestTamperDetected(t *testing.T) {
	body, err := Encode(sample(), keyA, "")
	require.NoError(t, err)
	payload, sig, _ := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(body, markerOpen), markerClose), ".")
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	require.NoError(t, err)

	t.Run("changed field", func(t *testing.T) {
		forged := strings.Replace(string(raw), `"stage":"implement"`, `"stage":"done"`, 1)
		require.NotEqual(t, string(raw), forged)
		_, err := Decode(markerOpen+base64.RawURLEncoding.EncodeToString([]byte(forged))+"."+sig+markerClose, keyA)
		assert.ErrorIs(t, err, ErrBadSignature)
	})
	t.Run("changed signature", func(t *testing.T) {
		flipped := []byte(sig)
		flipped[0] ^= 1
		_, err := Decode(markerOpen+payload+"."+string(flipped)+markerClose, keyA)
		assert.Error(t, err)
	})
	t.Run("signature from another key", func(t *testing.T) {
		other, err := Encode(sample(), keyB, "")
		require.NoError(t, err)
		_, err = Decode(other, keyA)
		assert.ErrorIs(t, err, ErrBadSignature)
	})
	t.Run("unknown kid", func(t *testing.T) {
		_, err := Decode(body, keyB)
		assert.ErrorIs(t, err, ErrBadSignature)
	})
}

func TestKeyRotationAcceptsOldKey(t *testing.T) {
	old, err := Encode(sample(), keyA, "")
	require.NoError(t, err)
	_, err = Decode(old, keyB, keyA)
	assert.NoError(t, err, "the previous key stays valid while it is listed")
	_, err = Decode(old, keyB)
	assert.ErrorIs(t, err, ErrBadSignature, "an unlisted key is no longer valid")
}

func TestMalformedInputs(t *testing.T) {
	good, err := Encode(sample(), keyA, "")
	require.NoError(t, err)
	cases := map[string]string{
		"two markers":      good + "\n" + good,
		"truncated":        good[:len(good)-12],
		"no dot":           markerOpen + "abcd" + markerClose,
		"bad base64":       markerOpen + "!!!.???" + markerClose,
		"marker in prose":  "see " + markerOpen + " for details\n" + good,
		"not json":         markerOpen + base64.RawURLEncoding.EncodeToString([]byte("nope")) + ".AAAA" + markerClose,
		"unknown field":    markerOpen + base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"extra":1}`)) + ".AAAA" + markerClose,
		"trailing garbage": markerOpen + base64.RawURLEncoding.EncodeToString([]byte(`{"v":1} {"v":1}`)) + ".AAAA" + markerClose,
		"wrong version":    markerOpen + base64.RawURLEncoding.EncodeToString([]byte(`{"v":2}`)) + ".AAAA" + markerClose,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Decode(body, keyA)
			require.Error(t, err)
			assert.False(t, errors.Is(err, ErrNoState), "a present marker is never 'no state'")
		})
	}
	_, err = Decode("just a human comment", keyA)
	assert.ErrorIs(t, err, ErrNoState)
}

func TestSizeBudget(t *testing.T) {
	s := sample()
	s.Counters = map[string]int64{strings.Repeat("k", MaxPayload): 1}
	_, err := Encode(s, keyA, "")
	assert.ErrorIs(t, err, ErrTooLarge)

	huge := markerOpen + strings.Repeat("A", 4*MaxPayload) + ".AAAA" + markerClose
	_, err = Decode(huge, keyA)
	assert.ErrorIs(t, err, ErrTooLarge)

	_, err = Encode(sample(), keyA, "text "+markerOpen+"x")
	assert.Error(t, err, "human text cannot smuggle a second marker")
	_, err = Encode(sample(), core.Secret{}, "")
	assert.Error(t, err)
}

func TestVerifyChecksAuthorAndRun(t *testing.T) {
	body, err := Encode(sample(), keyA, "")
	require.NoError(t, err)

	_, err = Verify(comment(body), trusted, "acme-api-12", keyA)
	assert.NoError(t, err)

	forgedAuthor := comment(body)
	forgedAuthor.AuthorID = 9
	_, err = Verify(forgedAuthor, trusted, "acme-api-12", keyA)
	assert.ErrorIs(t, err, ErrUntrustedAuthor, "a copied valid comment from another user is rejected")

	wrongApp := comment(body)
	wrongApp.AppID = 0
	_, err = Verify(wrongApp, trusted, "acme-api-12", keyA)
	assert.ErrorIs(t, err, ErrUntrustedAuthor, "a user comment cannot stand in for an App comment")

	_, err = Verify(comment(body), trusted, "acme-api-99", keyA)
	assert.ErrorIs(t, err, ErrWrongRun, "state of one run is not valid for another")

	_, err = Verify(comment(body), Author{}, "acme-api-12", keyA)
	assert.ErrorIs(t, err, ErrUntrustedAuthor, "an unset trusted identity trusts nobody")
}

func TestLatestPicksHighestAuthenticSeq(t *testing.T) {
	enc := func(seq uint64, key core.Secret) string {
		s := sample()
		s.Seq = seq
		b, err := Encode(s, key, "")
		require.NoError(t, err)
		return b
	}
	forged := Comment{ID: 4, AuthorID: 9, Body: enc(99, keyA)}
	badSig := Comment{ID: 5, AuthorID: trusted.UserID, AppID: trusted.AppID, Body: enc(100, keyB)}
	comments := []Comment{
		{ID: 1, AuthorID: trusted.UserID, AppID: trusted.AppID, Body: enc(3, keyA)},
		{ID: 2, AuthorID: trusted.UserID, AppID: trusted.AppID, Body: enc(7, keyA)},
		{ID: 3, AuthorID: trusted.UserID, AppID: trusted.AppID, Body: "a human reply"},
		forged, badSig,
	}
	got, id, err := Latest(comments, trusted, "acme-api-12", keyA)
	require.NoError(t, err)
	assert.EqualValues(t, 7, got.Seq)
	assert.EqualValues(t, 2, id)

	_, _, err = Latest([]Comment{forged, comments[2]}, trusted, "acme-api-12", keyA)
	assert.ErrorIs(t, err, ErrNoState, "only forgeries and prose means no state")
	_, _, err = Latest([]Comment{badSig}, trusted, "acme-api-12", keyA)
	assert.ErrorIs(t, err, ErrBadSignature, "an authored comment that fails the signature is reported")
}

func TestAdvanceAndReplay(t *testing.T) {
	cur := sample()
	next := cur
	next.Stage = "verify"
	got, err := Advance(cur, next)
	require.NoError(t, err)
	assert.EqualValues(t, 6, got.Seq)
	assert.NoError(t, CheckReplay(cur, got))
	assert.ErrorIs(t, CheckReplay(got, cur), ErrReplay, "an older comment restored by an edit is a replay")
	assert.ErrorIs(t, CheckReplay(cur, cur), ErrReplay, "an equal seq is not newer")

	other := next
	other.Run = "someone-else"
	_, err = Advance(cur, other)
	assert.ErrorIs(t, err, ErrWrongRun)
}

func FuzzDecodeNeverPanics(f *testing.F) {
	good, _ := Encode(sample(), keyA, "hi")
	f.Add(good)
	f.Add(markerOpen + "x.y" + markerClose)
	f.Add("")
	f.Fuzz(func(t *testing.T, body string) {
		s, err := Decode(body, keyA)
		if err == nil {
			// Anything accepted must re-encode to something that decodes to the same state.
			again, encErr := Encode(s, keyA, "")
			require.NoError(t, encErr)
			back, decErr := Decode(again, keyA)
			require.NoError(t, decErr)
			assert.Equal(t, s, back)
		}
	})
}
