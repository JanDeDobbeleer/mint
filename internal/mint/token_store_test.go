package mint

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testSecretPrompter struct {
	values        []string
	buffers       [][]byte
	messages      []string
	confirmAnswer bool
	confirmCalls  int
}

func (p *testSecretPrompter) password(message string) ([]byte, error) {
	p.messages = append(p.messages, message)
	if len(p.values) == 0 {
		return nil, errors.New("no test secret available")
	}
	value := []byte(p.values[0])
	p.values = p.values[1:]
	p.buffers = append(p.buffers, value)
	return value, nil
}

func (p *testSecretPrompter) confirm(message string) (bool, error) {
	p.messages = append(p.messages, message)
	p.confirmCalls++
	return p.confirmAnswer, nil
}

func TestEncryptedRepositoryTokenIsBoundToRepositoryAndPassphrase(t *testing.T) {
	repo := repository{Owner: "owner", Name: "project"}
	token := []byte("github_pat_secret")
	password := []byte("long test passphrase")
	path := tokenFilePath(t.TempDir(), repo)
	require.NoError(t, writeEncryptedToken(path, repo, token, password, false))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(data), string(token))
	assert.NotContains(t, string(data), string(password))
	decoded, err := readEncryptedToken(path, repo, password)
	require.NoError(t, err)
	assert.Equal(t, token, decoded)
	clear(decoded)

	_, err = readEncryptedToken(path, repository{Owner: "owner", Name: "other"}, password)
	require.ErrorContains(t, err, "cannot decrypt")
	_, err = readEncryptedToken(path, repo, []byte("wrong passphrase"))
	require.ErrorContains(t, err, "cannot decrypt")

	assert.Equal(t, path, tokenFilePath(filepath.Dir(filepath.Dir(path)), repository{Owner: "OWNER", Name: "PROJECT"}))
}

func TestRepositoryTokenCreationAndReuse(t *testing.T) {
	dir := t.TempDir()
	repo := repository{Owner: "owner", Name: "project"}
	var requests int
	client := &githubClient{
		base: "https://api.github.test",
		http: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			assert.Equal(t, http.MethodGet, request.Method)
			assert.Equal(t, "/repos/owner/project", request.URL.Path)
			assert.Equal(t, "Bearer github_pat_test", request.Header.Get("Authorization"))
			return testHTTPResponse(request, http.StatusOK, nil, `{}`), nil
		})},
	}
	prompts := &testSecretPrompter{values: []string{"github_pat_test", "store-passphrase", "store-passphrase"}}
	var stderr bytes.Buffer
	token, err := repositoryToken(context.Background(), client, dir, repo, prompts, &stderr)
	require.NoError(t, err)
	assert.Equal(t, "github_pat_test", token)
	assert.Equal(t, 1, requests)
	assert.Contains(t, stderr.String(), "https://github.com/settings/personal-access-tokens/new?")
	assert.Contains(t, stderr.String(), "Select only this repository")
	assert.NotContains(t, stderr.String(), token)
	assert.NotContains(t, stderr.String(), "store-passphrase")
	assert.Equal(t, 3, len(prompts.buffers))
	for _, buffer := range prompts.buffers {
		assert.Equal(t, make([]byte, len(buffer)), buffer)
	}

	prompts = &testSecretPrompter{values: []string{"store-passphrase"}}
	token, err = repositoryToken(context.Background(), client, dir, repo, prompts, &stderr)
	require.NoError(t, err)
	assert.Equal(t, "github_pat_test", token)
	assert.Equal(t, 2, requests)
	assert.Equal(t, []string{"Mint token-store passphrase: "}, prompts.messages)
}

func TestRepositoryTokenReplacesRevokedTokenWithoutRevokingNewToken(t *testing.T) {
	dir := t.TempDir()
	repo := repository{Owner: "owner", Name: "project"}
	path := tokenFilePath(dir, repo)
	require.NoError(t, writeEncryptedToken(path, repo, []byte("github_pat_revoked"), []byte("store-passphrase"), false))

	requests := 0
	client := &githubClient{
		base: "https://api.github.test",
		http: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			if requests == 1 {
				assert.Equal(t, "Bearer github_pat_revoked", request.Header.Get("Authorization"))
				return testHTTPResponse(request, http.StatusUnauthorized, nil, ""), nil
			}
			assert.Equal(t, "Bearer github_pat_replacement", request.Header.Get("Authorization"))
			return testHTTPResponse(request, http.StatusOK, nil, `{}`), nil
		})},
	}
	prompts := &testSecretPrompter{values: []string{"store-passphrase", "github_pat_replacement"}}
	var stderr bytes.Buffer
	token, err := repositoryToken(context.Background(), client, dir, repo, prompts, &stderr)
	require.NoError(t, err)
	assert.Equal(t, "github_pat_replacement", token)
	assert.Contains(t, stderr.String(), "create a replacement token")
	assert.NotContains(t, stderr.String(), "github_pat_replacement")
	assert.Equal(t, 2, requests)

	decoded, err := readEncryptedToken(path, repo, []byte("store-passphrase"))
	require.NoError(t, err)
	assert.Equal(t, "github_pat_replacement", string(decoded))
	clear(decoded)
}

func TestRepositoryTokenDoesNotReplaceOnNetworkFailure(t *testing.T) {
	dir := t.TempDir()
	repo := repository{Owner: "owner", Name: "project"}
	path := tokenFilePath(dir, repo)
	require.NoError(t, writeEncryptedToken(path, repo, []byte("github_pat_saved"), []byte("store-passphrase"), false))
	client := &githubClient{
		base: "https://api.github.test",
		http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("offline")
		})},
	}
	prompts := &testSecretPrompter{values: []string{"store-passphrase"}}
	_, err := repositoryToken(context.Background(), client, dir, repo, prompts, &bytes.Buffer{})
	require.ErrorContains(t, err, "connectivity")
	assert.Len(t, prompts.values, 0)
	assert.Len(t, prompts.messages, 1)

	decoded, err := readEncryptedToken(path, repo, []byte("store-passphrase"))
	require.NoError(t, err)
	assert.Equal(t, "github_pat_saved", string(decoded))
	clear(decoded)
}

func TestTokenCreationURLPrefillsRepositoryAndPermissionDefaults(t *testing.T) {
	link := tokenCreationURL(repository{Owner: "owner", Name: "project"})
	assert.True(t, strings.HasPrefix(link, "https://github.com/settings/personal-access-tokens/new?"))
	assert.Contains(t, link, "target_name=owner")
	assert.Contains(t, link, "expires_in=30")
	assert.Contains(t, link, "contents=write")
	assert.Contains(t, link, "pull_requests=write")
	assert.Contains(t, link, "issues=write")
	assert.Contains(t, link, "workflows=write")
}

func TestFineGrainedTokenFormat(t *testing.T) {
	for _, tt := range []struct {
		name  string
		token string
		valid bool
	}{
		{name: "fine-grained token", token: "github_pat_value", valid: true},
		{name: "classic PAT", token: "ghp_value"},
		{name: "whitespace", token: "github_pat_bad value"},
		{name: "empty"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, isFineGrainedToken([]byte(tt.token)))
		})
	}
}
