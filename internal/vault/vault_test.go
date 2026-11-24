package vault

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/JanDeDobbeleer/mint/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fastKDF lowers the PBKDF2 cost for tests that create their own files. Tests using it must not run in parallel.
func fastKDF(t *testing.T) {
	t.Helper()
	old := kdfIterations
	kdfIterations = 1000
	t.Cleanup(func() { kdfIterations = old })
}

type fakePrompter struct {
	values   []string
	buffers  [][]byte
	messages []string
}

func (p *fakePrompter) Password(message string) ([]byte, error) {
	p.messages = append(p.messages, message)
	if len(p.values) == 0 {
		return nil, errors.New("no test password available")
	}
	value := []byte(p.values[0])
	p.values = p.values[1:]
	p.buffers = append(p.buffers, value)
	return value, nil
}

func newVault(t *testing.T) (*Vault, string) {
	t.Helper()
	fastKDF(t)
	dir := t.TempDir()
	v, err := Unlock(dir, &fakePrompter{values: []string{"master", "master"}})
	require.NoError(t, err)
	t.Cleanup(v.Close)
	return v, dir
}

func reopen(t *testing.T, dir, password string) (*Vault, error) {
	t.Helper()
	return Unlock(dir, &fakePrompter{values: []string{password}})
}

func TestNamedCredentialRoundTrip(t *testing.T) {
	v, dir := newVault(t)
	require.NoError(t, v.Put(CopilotToken, core.NewSecretString("github_pat_copilot"), false))

	got, err := v.Get(CopilotToken)
	require.NoError(t, err)
	assert.Equal(t, "github_pat_copilot", got.Reveal())

	raw, err := os.ReadFile(filepath.Join(dir, "credentials", "copilot-token.enc"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "github_pat_copilot")
	assert.True(t, bytes.HasPrefix(raw, []byte("MINTPAT1")))

	reopened, err := reopen(t, dir, "master")
	require.NoError(t, err)
	defer reopened.Close()
	again, err := reopened.Get(CopilotToken)
	require.NoError(t, err)
	assert.Equal(t, "github_pat_copilot", again.Reveal())
}

func TestPutRefusesToOverwriteWithoutReplace(t *testing.T) {
	v, _ := newVault(t)
	require.NoError(t, v.Put(WebhookSecret, core.NewSecretString("one"), false))
	err := v.Put(WebhookSecret, core.NewSecretString("two"), false)
	require.ErrorContains(t, err, "already exists")
	got, err := v.Get(WebhookSecret)
	require.NoError(t, err)
	assert.Equal(t, "one", got.Reveal())
	require.NoError(t, v.Put(WebhookSecret, core.NewSecretString("two"), true))
	got, err = v.Get(WebhookSecret)
	require.NoError(t, err)
	assert.Equal(t, "two", got.Reveal())
}

func TestMissingCredentialIsNotFound(t *testing.T) {
	v, _ := newVault(t)
	_, err := v.Get(WebhookSecret)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, v.Delete(WebhookSecret), ErrNotFound)
}

func TestWrongPasswordNeverChangesData(t *testing.T) {
	v, dir := newVault(t)
	require.NoError(t, v.Put(WebhookSecret, core.NewSecretString("value"), false))
	snapshot := snapshotTree(t, dir)

	_, err := reopen(t, dir, "wrong password")
	require.ErrorContains(t, err, "cannot unlock")
	assert.Equal(t, snapshot, snapshotTree(t, dir), "a failed unlock must not modify, add or remove any file")
}

func TestTamperedAndMovedFilesFailToDecrypt(t *testing.T) {
	v, dir := newVault(t)
	require.NoError(t, v.Put(WebhookSecret, core.NewSecretString("webhook"), false))
	require.NoError(t, v.Put(CopilotToken, core.NewSecretString("copilot"), false))
	credentials := filepath.Join(dir, "credentials")
	original, err := os.ReadFile(filepath.Join(credentials, "webhook-secret.enc"))
	require.NoError(t, err)

	t.Run("credential file copied over another name", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(credentials, "copilot-token.enc"), original, 0o600))
		_, err := v.Get(CopilotToken)
		require.ErrorContains(t, err, "cannot decrypt")
	})

	t.Run("every byte flipped", func(t *testing.T) {
		for i := range original {
			mutated := bytes.Clone(original)
			mutated[i] ^= 0x01
			require.NoError(t, os.WriteFile(filepath.Join(credentials, "webhook-secret.enc"), mutated, 0o600))
			_, err := v.Get(WebhookSecret)
			require.Error(t, err, "flipping byte %d must be detected", i)
		}
	})

	t.Run("truncated and extended", func(t *testing.T) {
		for _, data := range [][]byte{nil, original[:8], original[:len(original)-1], append(bytes.Clone(original), 0)} {
			require.NoError(t, os.WriteFile(filepath.Join(credentials, "webhook-secret.enc"), data, 0o600))
			_, err := v.Get(WebhookSecret)
			require.Error(t, err)
		}
	})

	t.Run("oversized file", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(credentials, "webhook-secret.enc"), make([]byte, maxFileSize+1), 0o600))
		_, err := v.Get(WebhookSecret)
		require.ErrorContains(t, err, "invalid encrypted credential file")
	})
}

func TestAssociatedDataBindsRepositoryAndCredential(t *testing.T) {
	fastKDF(t)
	key := []byte("0123456789abcdef0123456789abcdef")
	sealed, err := Seal([]byte("secret"), key, RepoTokenAD("owner/project"))
	require.NoError(t, err)
	plain, err := Open(sealed, key, RepoTokenAD("owner/project"))
	require.NoError(t, err)
	assert.Equal(t, "secret", string(plain))
	for name, ad := range map[string][]byte{
		"other repository":     RepoTokenAD("owner/other"),
		"credential namespace": credentialAD("owner/project"),
		"master key context":   []byte(masterContext),
		"no associated data":   nil,
		"case differs":         RepoTokenAD("Owner/project"),
	} {
		_, err := Open(sealed, key, ad)
		require.Error(t, err, name)
	}
	_, err = Open(sealed, []byte("a different key of 32 bytes......"), RepoTokenAD("owner/project"))
	require.Error(t, err)
}

func TestSealUsesFreshRandomnessEveryTime(t *testing.T) {
	fastKDF(t)
	a, err := Seal([]byte("same"), []byte("pw"), nil)
	require.NoError(t, err)
	b, err := Seal([]byte("same"), []byte("pw"), nil)
	require.NoError(t, err)
	assert.NotEqual(t, a, b)
	_, err = Seal([]byte("x"), nil, nil)
	require.Error(t, err)
}

func TestListShowsNamesAndMetadataOnlyWithoutAPassword(t *testing.T) {
	v, dir := newVault(t)
	require.NoError(t, v.Put(WebhookSecret, core.NewSecretString("topsecretvalue"), false))
	require.NoError(t, v.Put("custom-thing", core.NewSecretString("topsecretvalue2"), false))
	v.Close()

	infos, err := List(dir)
	require.NoError(t, err)
	require.Len(t, infos, 2)
	assert.Equal(t, "custom-thing", infos[0].Name)
	assert.False(t, infos[0].Known)
	assert.Equal(t, WebhookSecret, infos[1].Name)
	assert.True(t, infos[1].Known)
	assert.False(t, infos[1].Updated.IsZero())
	assert.NotContains(t, fmt.Sprintf("%+v", infos), "topsecretvalue")

	empty, err := List(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestListIgnoresStrayFiles(t *testing.T) {
	_, dir := newVault(t)
	credentials := filepath.Join(dir, "credentials")
	require.NoError(t, os.MkdirAll(credentials, 0o700))
	for _, name := range []string{".credential-123.tmp", "Bad Name.enc", "notes.txt", "UPPER.enc"} {
		require.NoError(t, os.WriteFile(filepath.Join(credentials, name), []byte("x"), 0o600))
	}
	require.NoError(t, os.Mkdir(filepath.Join(credentials, "dir.enc"), 0o700))
	infos, err := List(dir)
	require.NoError(t, err)
	assert.Empty(t, infos)
}

func TestDeleteWorksWithoutUnlocking(t *testing.T) {
	v, dir := newVault(t)
	require.NoError(t, v.Put(WebhookSecret, core.NewSecretString("x"), false))
	v.Close()
	require.NoError(t, DeleteCredential(dir, WebhookSecret))
	infos, err := List(dir)
	require.NoError(t, err)
	assert.Empty(t, infos)
}

func TestNameValidation(t *testing.T) {
	for _, name := range []string{"copilot-token", "a1", "email-imap-password"} {
		require.NoError(t, ValidName(name), name)
	}
	for _, name := range []string{"", "a", "Upper", "-lead", "trail-", "dou--ble", "with space", "../escape", "a/b", `a\b`, "dot.name",
		"con", "nul", "com1", "lpt9", "1starts", strings.Repeat("a", 65), "name\x00", "name:stream"} {
		require.Error(t, ValidName(name), name)
	}
	v, _ := newVault(t)
	require.Error(t, v.Put("../escape", core.NewSecretString("x"), false))
	_, err := v.Get("../escape")
	require.Error(t, err)
}

func TestEmptyCredentialIsRejected(t *testing.T) {
	v, _ := newVault(t)
	require.ErrorContains(t, v.Put(WebhookSecret, core.Secret{}, false), "empty")
}

func TestClosedVaultIsLockedAndZeroized(t *testing.T) {
	v, _ := newVault(t)
	key := v.key
	v.Close()
	assert.Equal(t, make([]byte, keySize), key)
	require.ErrorContains(t, v.Put(WebhookSecret, core.NewSecretString("x"), false), "locked")
	_, err := v.Get(WebhookSecret)
	require.ErrorContains(t, err, "locked")
	_, err = v.StateSigningKey()
	require.ErrorContains(t, err, "locked")
	require.ErrorContains(t, v.Rotate(WebhookSecret, nil), "locked")
}

// --- runtime write path ---

func TestRotateReplacesTheValueAtomically(t *testing.T) {
	v, dir := newVault(t)
	require.NoError(t, v.Put(EmailOAuthRefresh, core.NewSecretString("refresh-1"), false))
	require.NoError(t, v.Rotate(EmailOAuthRefresh, func(current core.Secret) (core.Secret, error) {
		assert.Equal(t, "refresh-1", current.Reveal())
		return core.NewSecretString("refresh-2"), nil
	}))
	got, err := v.Get(EmailOAuthRefresh)
	require.NoError(t, err)
	assert.Equal(t, "refresh-2", got.Reveal())
	assertNoTempFiles(t, dir)
}

func TestFailedRotationKeepsTheOldValue(t *testing.T) {
	v, dir := newVault(t)
	require.NoError(t, v.Put(EmailOAuthRefresh, core.NewSecretString("refresh-1"), false))
	before := snapshotTree(t, dir)

	err := v.Rotate(EmailOAuthRefresh, func(core.Secret) (core.Secret, error) { return core.Secret{}, errors.New("provider refused") })
	require.ErrorContains(t, err, "provider refused")
	err = v.Rotate(EmailOAuthRefresh, func(core.Secret) (core.Secret, error) { return core.Secret{}, nil })
	require.ErrorContains(t, err, "empty")
	assert.Equal(t, before, snapshotTree(t, dir))

	require.ErrorIs(t, v.Rotate(WebhookSecret, func(c core.Secret) (core.Secret, error) { return c, nil }), ErrNotFound)
}

func TestRotateFailsWithoutPartialWriteWhenTheTargetIsNotAFile(t *testing.T) {
	v, dir := newVault(t)
	require.NoError(t, v.Put(EmailOAuthRefresh, core.NewSecretString("refresh-1"), false))
	path := filepath.Join(dir, "credentials", "email-oauth-refresh-token.enc")
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Mkdir(path, 0o700))
	err := v.Put(EmailOAuthRefresh, core.NewSecretString("x"), true)
	require.Error(t, err)
	assertNoTempFiles(t, dir)
}

func TestConcurrentRotationsLoseNoUpdates(t *testing.T) {
	v, dir := newVault(t)
	require.NoError(t, v.Put(EmailOAuthRefresh, core.NewSecretString("0"), false))
	const workers = 8
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			err := v.Rotate(EmailOAuthRefresh, func(current core.Secret) (core.Secret, error) {
				var n int
				_, err := fmt.Sscanf(current.Reveal(), "%d", &n)
				return core.NewSecretString(fmt.Sprint(n + 1)), err
			})
			assert.NoError(t, err)
		})
	}
	wg.Wait()
	got, err := v.Get(EmailOAuthRefresh)
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprint(workers), got.Reveal())
	assertNoTempFiles(t, dir)
}

// --- HKDF ---

func TestDerivedKeysAreStableIndependentAndTiedToTheVault(t *testing.T) {
	v, dir := newVault(t)
	a, err := v.StateSigningKey()
	require.NoError(t, err)
	b, err := v.StateSigningKey()
	require.NoError(t, err)
	assert.Equal(t, a.Reveal(), b.Reveal())
	assert.Len(t, a.Reveal(), 32)

	other, err := v.DeriveKey("mint:other:v1", 32)
	require.NoError(t, err)
	assert.NotEqual(t, a.Reveal(), other.Reveal())

	reopened, err := reopen(t, dir, "master")
	require.NoError(t, err)
	defer reopened.Close()
	again, err := reopened.StateSigningKey()
	require.NoError(t, err)
	assert.Equal(t, a.Reveal(), again.Reveal(), "the signing key survives restarts")

	v2, _ := newVault(t)
	different, err := v2.StateSigningKey()
	require.NoError(t, err)
	assert.NotEqual(t, a.Reveal(), different.Reveal(), "another vault derives another key")
	assert.NotContains(t, fmt.Sprintf("%v %+v %#v", a, a, a), a.Reveal())
}

// --- backward compatibility with the original repository-token store ---

func TestLegacyRepositoryTokenStoreStillOpens(t *testing.T) {
	// Fixtures were produced by the pre-vault token_store.go / master_password.go (main at 0df1aa0):
	// master password "fixture-master-password", store key "0123456789abcdef0123456789abcdef",
	// a token for Owner/Project. Production PBKDF2 cost is used on purpose (no fastKDF).
	dir := t.TempDir()
	copyTree(t, filepath.Join("testdata", "legacy"), dir)

	prompter := &fakePrompter{values: []string{"fixture-master-password"}}
	v, err := Unlock(dir, prompter)
	require.NoError(t, err)
	defer v.Close()
	assert.Equal(t, []string{"Mint master password: "}, prompter.messages)

	token, err := v.GetRepoToken(RepoIdentity("Owner", "Project"))
	require.NoError(t, err)
	assert.Equal(t, "github_pat_fixture", token.Reveal())
	assert.Equal(t, filepath.Join(dir, "tokens", "90829ce5ea7850daa54bb09879038a2cefef2540025072f74bca2ede43a07af7.enc"),
		RepoTokenPath(dir, RepoIdentity("owner", "project")), "token file locations are unchanged")

	_, err = v.GetRepoToken(RepoIdentity("owner", "other"))
	require.Error(t, err)

	_, err = Unlock(dir, &fakePrompter{values: []string{"wrong"}})
	require.ErrorContains(t, err, "cannot unlock")

	// Legacy data and new credentials live side by side under the same master password.
	require.NoError(t, v.Put(WebhookSecret, core.NewSecretString("new"), false))
	again, err := v.GetRepoToken(RepoIdentity("owner", "project"))
	require.NoError(t, err)
	assert.Equal(t, "github_pat_fixture", again.Reveal())
}

func TestNewTokenFilesStayReadableByTheLegacyLayout(t *testing.T) {
	v, dir := newVault(t)
	require.NoError(t, v.PutRepoToken("owner/project", core.NewSecretString("github_pat_new"), false))
	raw, err := ReadFile(RepoTokenPath(dir, "owner/project"), v.key, []byte("MINTPAT1\x00owner/project"))
	require.NoError(t, err)
	assert.Equal(t, "github_pat_new", string(raw))
	require.Error(t, v.PutRepoToken("owner/project", core.NewSecretString("x"), false), "no replace without the flag")
}

// --- first-use creation ---

func TestMasterPasswordCreation(t *testing.T) {
	fastKDF(t)
	for _, passwords := range [][]string{{""}, {"password", "different"}} {
		dir := t.TempDir()
		prompter := &fakePrompter{values: passwords}
		_, err := Unlock(dir, prompter)
		require.Error(t, err)
		_, statErr := os.Stat(filepath.Join(dir, "master.enc"))
		require.ErrorIs(t, statErr, os.ErrNotExist)
		for _, buffer := range prompter.buffers {
			assert.Equal(t, make([]byte, len(buffer)), buffer, "password buffers are zeroized")
		}
	}
	dir := t.TempDir()
	prompter := &fakePrompter{values: []string{"pw", "pw"}}
	v, err := Unlock(dir, prompter)
	require.NoError(t, err)
	v.Close()
	assert.Equal(t, []string{"Create Mint master password: ", "Confirm Mint master password: "}, prompter.messages)
	for _, buffer := range prompter.buffers {
		assert.Equal(t, make([]byte, len(buffer)), buffer)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "master.enc"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "pw")
}

func TestStdinPrompterSupportsCreationAndUnlock(t *testing.T) {
	fastKDF(t)
	dir := t.TempDir()
	first := NewStdinPrompter(strings.NewReader("stdin password\r\nignored second line\n"))
	v, err := Unlock(dir, first)
	require.NoError(t, err, "creation asks twice and both answers come from the one line")
	v.Close()
	first.Close()

	second := NewStdinPrompter(strings.NewReader("stdin password"))
	v, err = Unlock(dir, second)
	require.NoError(t, err, "a last line without a newline is accepted")
	v.Close()

	_, err = Unlock(dir, NewStdinPrompter(strings.NewReader("wrong\n")))
	require.Error(t, err)
	_, err = Unlock(dir, NewStdinPrompter(strings.NewReader("")))
	require.ErrorContains(t, err, "standard input")
	_, err = Unlock(dir, NewStdinPrompter(strings.NewReader(strings.Repeat("x", maxPasswordLine+10)+"\n")))
	require.ErrorContains(t, err, "too long")
}

// --- credential specs ---

func TestKnownCredentialSpecs(t *testing.T) {
	want := []string{"github-app-private-key", "github-operator-pat", "copilot-token", "email-imap-password",
		"email-oauth-refresh-token", "email-oauth-client-secret", "whatsapp-token", "whatsapp-app-secret",
		"whatsapp-verify-token", "webhook-secret"}
	var got []string
	for _, spec := range Known() {
		got = append(got, spec.Name)
		require.NoError(t, ValidName(spec.Name))
		assert.NotEmpty(t, spec.Description)
	}
	assert.Equal(t, want, got)
	assert.True(t, IsKnown("copilot-token"))
	assert.False(t, IsKnown("nope"))
}

func TestSpecChecks(t *testing.T) {
	copilot, _ := Lookup(CopilotToken)
	require.NoError(t, copilot.Check("github_pat_abc"))
	require.ErrorContains(t, copilot.Check("ghp_classic"), "classic")
	require.ErrorContains(t, copilot.Check("has space"), "whitespace")
	require.ErrorContains(t, copilot.Check(""), "empty")
	require.ErrorContains(t, copilot.Check(strings.Repeat("a", maxFileSize)), "too large")

	pat, _ := Lookup(GitHubOperatorPAT)
	require.NoError(t, pat.Check("github_pat_abc"))
	require.Error(t, pat.Check("ghp_abc"))

	key := testPEM(t)
	appKey, _ := Lookup(GitHubAppPrivateKey)
	require.NoError(t, appKey.Check(key))
	require.Error(t, appKey.Check("-----BEGIN PRIVATE KEY-----\nnot base64!\n-----END PRIVATE KEY-----"))
	require.Error(t, appKey.Check(key+key), "exactly one block")
	require.Error(t, appKey.Check("plain text"))
	certificate := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("x")}))
	require.Error(t, appKey.Check(certificate), "only private keys")
}

func testPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// --- helpers ---

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		rel, _ := filepath.Rel(root, path)
		files[rel] = string(data)
		return err
	}))
	return files
}

func assertNoTempFiles(t *testing.T, root string) {
	t.Helper()
	for name := range snapshotTree(t, root) {
		assert.NotContains(t, name, ".tmp", "no staged file may be left behind")
	}
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	require.NoError(t, filepath.WalkDir(from, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		target := filepath.Join(to, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	}))
}
