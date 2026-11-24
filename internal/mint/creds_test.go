package mint

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JanDeDobbeleer/mint/internal/vault"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type credsHarness struct {
	t       *testing.T
	prompts *testSecretPrompter
	dir     string
	stdin   string
	stdout  bytes.Buffer
	stderr  bytes.Buffer
}

func newCredsHarness(t *testing.T, values ...string) *credsHarness {
	t.Helper()
	return &credsHarness{t: t, dir: t.TempDir(), prompts: &testSecretPrompter{values: values}}
}

func (h *credsHarness) run(args ...string) error {
	h.t.Helper()
	return credsCommand(args, credsEnv{
		prompts: h.prompts, stdin: strings.NewReader(h.stdin), stdout: &h.stdout, stderr: &h.stderr, dir: h.dir,
	})
}

// assertNoLeak checks that no secret appeared in anything the command printed or in an error.
func (h *credsHarness) assertNoLeak(err error, secrets ...string) {
	h.t.Helper()
	out := h.stdout.String() + h.stderr.String()
	if err != nil {
		out += err.Error()
	}
	for _, secret := range secrets {
		assert.NotContains(h.t, out, secret)
	}
	for _, buffer := range h.prompts.buffers {
		assert.Equal(h.t, make([]byte, len(buffer)), buffer, "prompt buffers are zeroized")
	}
}

func openVault(t *testing.T, dir, password string) *vault.Vault {
	t.Helper()
	v, err := vault.Unlock(dir, passwordPrompter{&testSecretPrompter{values: []string{password}}})
	require.NoError(t, err)
	t.Cleanup(v.Close)
	return v
}

func TestCredsAddStoresAnEncryptedCredentialFromAHiddenPrompt(t *testing.T) {
	h := newCredsHarness(t, "master-pw", "master-pw", "github_pat_copilot_value")
	err := h.run("add", "copilot-token")
	require.NoError(t, err)
	assert.Equal(t, []string{"Create Mint master password: ", "Confirm Mint master password: ", "Enter copilot-token (input hidden): "}, h.prompts.messages)
	assert.Contains(t, h.stderr.String(), "Saved credential copilot-token.")
	h.assertNoLeak(nil, "github_pat_copilot_value", "master-pw")

	raw, err := os.ReadFile(filepath.Join(h.dir, "credentials", "copilot-token.enc"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "github_pat_copilot_value")
	got, err := openVault(t, h.dir, "master-pw").Get("copilot-token")
	require.NoError(t, err)
	assert.Equal(t, "github_pat_copilot_value", got.Reveal())
}

func TestCredsAddReadsTheMasterPasswordFromStandardInputOnly(t *testing.T) {
	h := newCredsHarness(t, "webhook-value")
	h.stdin = "stdin-master\n"
	require.NoError(t, h.run("add", "webhook-secret", "--password-stdin"))
	assert.Equal(t, []string{"Enter webhook-secret (input hidden): "}, h.prompts.messages, "only the value is prompted")
	h.assertNoLeak(nil, "webhook-value", "stdin-master")

	got, err := openVault(t, h.dir, "stdin-master").Get("webhook-secret")
	require.NoError(t, err)
	assert.Equal(t, "webhook-value", got.Reveal())

	// A second add unlocks with the same stdin source.
	h2 := &credsHarness{t: t, dir: h.dir, prompts: &testSecretPrompter{values: []string{"second-value"}}, stdin: "stdin-master\n"}
	require.NoError(t, h2.run("add", "whatsapp-token", "--password-stdin"))
}

func TestCredsRejectsSecretsInFlagsAndArgumentsWithoutEchoingThem(t *testing.T) {
	for _, args := range [][]string{
		{"add", "copilot-token", "--password=hunter2"},
		{"add", "copilot-token", "--password", "hunter2"},
		{"add", "copilot-token", "--value=hunter2"},
		{"add", "copilot-token", "-p", "hunter2"},
		{"add", "copilot-token", "--token=hunter2"},
		{"add", "copilot-token", "--password-stdin=hunter2"},
		{"add", "copilot-token", "hunter2"},
		{"add", "--value=hunter2", "copilot-token"},
		{"rm", "copilot-token", "hunter2"},
		{"rm", "copilot-token", "--password-stdin"},
		{"list", "hunter2"},
		{"list", "--password=hunter2"},
	} {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			h := newCredsHarness(t, "never-used")
			err := h.run(args...)
			require.Error(t, err)
			h.assertNoLeak(err, "hunter2")
			assert.Empty(t, h.prompts.messages, "nothing is prompted before the arguments are accepted")
			_, statErr := os.Stat(filepath.Join(h.dir, "master.enc"))
			require.ErrorIs(t, statErr, os.ErrNotExist, "no vault is created by a rejected command")
		})
	}
}

func TestCredsRejectsUnknownAndInvalidNames(t *testing.T) {
	for name, want := range map[string]string{
		"not-a-known-credential": "unknown credential name",
		"../escape":              "lower-case",
		"UPPER":                  "lower-case",
	} {
		h := newCredsHarness(t)
		err := h.run("add", name)
		require.ErrorContains(t, err, want)
		assert.Empty(t, h.prompts.messages)
	}
	for _, args := range [][]string{nil, {"unknown"}, {"add"}, {"rm"}} {
		require.Error(t, newCredsHarness(t).run(args...))
	}
	h := newCredsHarness(t)
	require.NoError(t, h.run("--help"))
	assert.Contains(t, h.stderr.String(), "github-app-private-key")
	assert.Contains(t, h.stderr.String(), "never accepted as flags")
}

func TestCredsAddValidatesTheValueBeforeStoring(t *testing.T) {
	h := newCredsHarness(t, "master", "master", "ghp_classic_token")
	err := h.run("add", "copilot-token")
	require.ErrorContains(t, err, "invalid copilot-token")
	h.assertNoLeak(err, "ghp_classic_token")
	infos, listErr := vault.List(h.dir)
	require.NoError(t, listErr)
	assert.Empty(t, infos)
}

func TestCredsAddWithWrongMasterPasswordChangesNothing(t *testing.T) {
	h := newCredsHarness(t, "right", "right", "value-one")
	require.NoError(t, h.run("add", "webhook-secret"))
	before, err := os.ReadFile(filepath.Join(h.dir, "credentials", "webhook-secret.enc"))
	require.NoError(t, err)
	master, err := os.ReadFile(filepath.Join(h.dir, "master.enc"))
	require.NoError(t, err)

	h2 := &credsHarness{t: t, dir: h.dir, prompts: &testSecretPrompter{values: []string{"wrong", "never-read"}, confirmAnswer: true}}
	err = h2.run("add", "webhook-secret")
	require.ErrorContains(t, err, "cannot unlock")
	assert.Zero(t, h2.prompts.confirmCalls, "a wrong password never reaches the replace prompt")
	after, err := os.ReadFile(filepath.Join(h.dir, "credentials", "webhook-secret.enc"))
	require.NoError(t, err)
	assert.Equal(t, before, after)
	masterAfter, err := os.ReadFile(filepath.Join(h.dir, "master.enc"))
	require.NoError(t, err)
	assert.Equal(t, master, masterAfter)
}

func TestCredsAddReplacesOnlyAfterConfirmation(t *testing.T) {
	h := newCredsHarness(t, "master", "master", "first-value")
	require.NoError(t, h.run("add", "webhook-secret"))

	declined := &credsHarness{t: t, dir: h.dir, prompts: &testSecretPrompter{values: []string{"master", "second-value"}}}
	require.ErrorContains(t, declined.run("add", "webhook-secret"), "kept")
	assert.Equal(t, 1, declined.prompts.confirmCalls)
	assert.Len(t, declined.prompts.values, 1, "the new value is never requested after declining")
	got, err := openVault(t, h.dir, "master").Get("webhook-secret")
	require.NoError(t, err)
	assert.Equal(t, "first-value", got.Reveal())

	accepted := &credsHarness{t: t, dir: h.dir, prompts: &testSecretPrompter{values: []string{"master", "second-value"}, confirmAnswer: true}}
	require.NoError(t, accepted.run("add", "webhook-secret"))
	got, err = openVault(t, h.dir, "master").Get("webhook-secret")
	require.NoError(t, err)
	assert.Equal(t, "second-value", got.Reveal())
}

func TestCredsAddOffersToReplaceADamagedCredential(t *testing.T) {
	h := newCredsHarness(t, "master", "master", "first-value")
	require.NoError(t, h.run("add", "webhook-secret"))
	path := filepath.Join(h.dir, "credentials", "webhook-secret.enc")
	require.NoError(t, os.WriteFile(path, []byte("garbage"), 0o600))

	declined := &credsHarness{t: t, dir: h.dir, prompts: &testSecretPrompter{values: []string{"master", "x"}}}
	require.ErrorContains(t, declined.run("add", "webhook-secret"), "kept")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "garbage", string(data))

	accepted := &credsHarness{t: t, dir: h.dir, prompts: &testSecretPrompter{values: []string{"master", "fixed-value"}, confirmAnswer: true}}
	require.NoError(t, accepted.run("add", "webhook-secret"))
	got, err := openVault(t, h.dir, "master").Get("webhook-secret")
	require.NoError(t, err)
	assert.Equal(t, "fixed-value", got.Reveal())
}

func TestCredsAddAcceptsAPastedPEMPrivateKey(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	block := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	lines := strings.Split(strings.TrimSpace(block), "\n")

	h := newCredsHarness(t, append([]string{"master", "master", ""}, lines...)...)
	require.NoError(t, h.run("add", "github-app-private-key"))
	h.assertNoLeak(nil, lines[1], "master")
	got, err := openVault(t, h.dir, "master").Get("github-app-private-key")
	require.NoError(t, err)
	assert.Equal(t, block, got.Reveal())

	bad := newCredsHarness(t, "master", "master", "-----BEGIN PRIVATE KEY-----", "bm90IGEga2V5", "-----END PRIVATE KEY-----")
	require.ErrorContains(t, bad.run("add", "github-app-private-key"), "invalid github-app-private-key")

	endless := newCredsHarness(t, append([]string{"master", "master"}, make([]string, maxPEMLines)...)...)
	require.ErrorContains(t, endless.run("add", "github-app-private-key"), "no END line")
}

func TestCredsListShowsNamesAndStatusOnly(t *testing.T) {
	h := newCredsHarness(t, "master", "master", "secret-webhook-value")
	require.NoError(t, h.run("add", "webhook-secret"))
	h.stdout.Reset()
	h.prompts.messages = nil

	require.NoError(t, h.run("list"))
	out := h.stdout.String()
	assert.Contains(t, out, "NAME")
	for _, spec := range vault.Known() {
		assert.Contains(t, out, spec.Name)
	}
	lines := map[string]string{}
	for line := range strings.SplitSeq(out, "\n") {
		if fields := strings.Fields(line); len(fields) > 1 {
			lines[fields[0]] = line
		}
	}
	assert.Contains(t, lines["webhook-secret"], "stored")
	assert.Contains(t, lines["copilot-token"], "not set")
	assert.NotContains(t, out, "secret-webhook-value")
	assert.Empty(t, h.prompts.messages, "list needs no password")

	require.NoError(t, os.WriteFile(filepath.Join(h.dir, "credentials", "stray-name.enc"), []byte("x"), 0o600))
	h.stdout.Reset()
	require.NoError(t, h.run("list"))
	assert.Contains(t, h.stdout.String(), "stray-name")
	assert.Contains(t, h.stdout.String(), "unknown")
}

func TestCredsListOnAnEmptyVault(t *testing.T) {
	h := newCredsHarness(t)
	require.NoError(t, h.run("list"))
	assert.Contains(t, h.stdout.String(), "not set")
	_, err := os.Stat(filepath.Join(h.dir, "master.enc"))
	require.ErrorIs(t, err, os.ErrNotExist, "list never creates a vault")
}

func TestCredsRemoveAsksForConfirmation(t *testing.T) {
	h := newCredsHarness(t, "master", "master", "value")
	require.NoError(t, h.run("add", "webhook-secret"))
	path := filepath.Join(h.dir, "credentials", "webhook-secret.enc")

	kept := &credsHarness{t: t, dir: h.dir, prompts: &testSecretPrompter{}}
	require.ErrorContains(t, kept.run("rm", "webhook-secret"), "kept")
	require.FileExists(t, path)

	removed := &credsHarness{t: t, dir: h.dir, prompts: &testSecretPrompter{confirmAnswer: true}}
	require.NoError(t, removed.run("rm", "webhook-secret"))
	require.NoFileExists(t, path)

	require.ErrorIs(t, removed.run("rm", "webhook-secret"), vault.ErrNotFound)
	require.ErrorContains(t, removed.run("rm", "../escape"), "lower-case")
}

func TestCredsThroughRunUsesTheConfigDirectory(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"APPDATA", "HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(name, base)
	}
	var stdout, stderr bytes.Buffer
	require.NoError(t, Run(t.Context(), []string{"creds", "list"}, "test-version", strings.NewReader(""), &stdout, &stderr))
	assert.Contains(t, stdout.String(), "copilot-token")
	err := Run(t.Context(), []string{"creds", "add", "copilot-token", "--password=hunter2"}, "test-version", strings.NewReader(""), &stdout, &stderr)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "hunter2")
}
