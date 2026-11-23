package mint

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMasterPasswordUnlocksMultipleRepositories(t *testing.T) {
	dir := t.TempDir()
	client := &githubClient{
		base: "https://api.github.test",
		http: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return testHTTPResponse(request, http.StatusOK, nil, `{}`), nil
		})},
	}
	first := repository{Owner: "owner", Name: "first"}
	second := repository{Owner: "owner", Name: "second"}
	prompts := &testSecretPrompter{values: []string{"one master password", "one master password", "github_pat_first"}}
	_, err := repositoryToken(context.Background(), client, dir, first, prompts, &bytes.Buffer{})
	require.NoError(t, err)

	prompts = &testSecretPrompter{values: []string{"one master password", "github_pat_second"}}
	token, err := repositoryToken(context.Background(), client, dir, second, prompts, &bytes.Buffer{})
	require.NoError(t, err)
	assert.Equal(t, "github_pat_second", token)
	assert.Equal(t, []string{"Mint master password: ", "Paste the fine-grained token (input hidden): "}, prompts.messages)

	prompts = &testSecretPrompter{values: []string{"one master password"}}
	token, err = repositoryToken(context.Background(), client, dir, first, prompts, &bytes.Buffer{})
	require.NoError(t, err)
	assert.Equal(t, "github_pat_first", token)
	assert.Equal(t, []string{"Mint master password: "}, prompts.messages)

	path := filepath.Join(dir, "master.enc")
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	prompts = &testSecretPrompter{values: []string{"wrong password"}}
	_, err = repositoryToken(context.Background(), client, dir, second, prompts, &bytes.Buffer{})
	require.ErrorContains(t, err, "cannot unlock the token store")
	assert.Zero(t, prompts.confirmCalls)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	assert.NotContains(t, string(after), "one master password")
}

func TestMasterPasswordCreationRejectsInvalidConfirmation(t *testing.T) {
	for _, passwords := range [][]string{{""}, {"password", "different"}} {
		dir := t.TempDir()
		prompts := &testSecretPrompter{values: passwords}
		_, err := unlockTokenStore(dir, prompts)
		require.Error(t, err)
		_, err = os.Stat(filepath.Join(dir, "master.enc"))
		require.ErrorIs(t, err, os.ErrNotExist)
		for _, buffer := range prompts.buffers {
			assert.Equal(t, make([]byte, len(buffer)), buffer)
		}
	}
}
