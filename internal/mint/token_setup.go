package mint

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
)

func tokenCreationURL(repo repository) string {
	values := url.Values{
		"name":          {"Mint"},
		"description":   {"Repository-scoped GitHub access for " + repo.Owner + "/" + repo.Name},
		"target_name":   {repo.Owner},
		"expires_in":    {"30"},
		"contents":      {"write"},
		"issues":        {"write"},
		"pull_requests": {"write"},
		"workflows":     {"write"},
	}
	return "https://github.com/settings/personal-access-tokens/new?" + values.Encode()
}

func repositoryToken(ctx context.Context, client *githubClient, dir string, repo repository, prompts secretPrompter, stderr io.Writer) (string, error) {
	ui := newTerminalUI(stderr)
	if err := ui.heading(repo.Owner + "/" + repo.Name); err != nil {
		return "", errors.New("cannot show repository details")
	}
	key, err := unlockTokenStore(dir, prompts)
	if err != nil {
		return "", err
	}
	defer clear(key)
	path := tokenFilePath(dir, repo)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return createRepositoryToken(ctx, client, path, repo, prompts, stderr, key, false)
	}
	if err != nil {
		return "", errors.New("cannot inspect the saved repository token")
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("saved repository token must be a regular file")
	}

	token, err := readEncryptedToken(path, repo, key)
	if err != nil {
		replace, promptErr := prompts.confirm("The saved repository token is damaged. Replace it? Type yes to replace [no]: ")
		if promptErr != nil {
			return "", promptErr
		}
		if !replace {
			return "", errors.New("cannot read the saved repository token")
		}
		return createRepositoryToken(ctx, client, path, repo, prompts, stderr, key, true)
	}
	defer clear(token)

	if !isFineGrainedToken(token) {
		if err := ui.status("!", "The saved credential is not a fine-grained PAT; create a replacement token."); err != nil {
			return "", errors.New("cannot report repository token status")
		}
		return createRepositoryToken(ctx, client, path, repo, prompts, stderr, key, true)
	}
	err = client.validateRepositoryToken(ctx, repo, string(token))
	if err == nil {
		return string(token), nil
	}
	if !repositoryTokenNeedsReplacement(err) {
		return "", err
	}
	if err := ui.status("!", "The saved token cannot access this repository; create a replacement token."); err != nil {
		return "", errors.New("cannot report repository token status")
	}
	return createRepositoryToken(ctx, client, path, repo, prompts, stderr, key, true)
}

func repositoryTokenNeedsReplacement(err error) bool {
	var apiErr *githubAPIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.status == 401 || apiErr.status == 404
}

func createRepositoryToken(ctx context.Context, client *githubClient, path string, repo repository, prompts secretPrompter, stderr io.Writer, key []byte, replace bool) (string, error) {
	ui := newTerminalUI(stderr)
	if err := ui.step("1", "Create a fine-grained token", tokenCreationURL(repo)); err != nil {
		return "", errors.New("cannot show token creation instructions")
	}
	if err := ui.step("2", "Select only this repository", repo.Owner+"/"+repo.Name+
		"\n     Contents, Issues, Pull requests, Workflows: write.\n     Remove permissions you do not need."); err != nil {
		return "", errors.New("cannot show token creation instructions")
	}
	if err := ui.step("3", "Save securely", "Token input stays hidden. Your master password protects every repository."); err != nil {
		return "", errors.New("cannot show token creation instructions")
	}
	input, err := prompts.password("Paste the fine-grained token (input hidden): ")
	if err != nil {
		return "", err
	}
	defer clear(input)
	token := bytes.TrimSpace(input)
	if !isFineGrainedToken(token) {
		return "", errors.New("invalid fine-grained token")
	}
	if err := client.validateRepositoryToken(ctx, repo, string(token)); err != nil {
		if _, ok := errors.AsType[*githubAPIError](err); ok {
			return "", fmt.Errorf("GitHub rejected the token for %s/%s: %w", repo.Owner, repo.Name, err)
		}
		return "", err
	}

	if err := writeEncryptedToken(path, repo, token, key, replace); err != nil {
		return "", err
	}
	if err := ui.status("+", "Saved encrypted token for "+repo.Owner+"/"+repo.Name+"."); err != nil {
		return "", errors.New("cannot report repository token status")
	}
	return string(token), nil
}

func isFineGrainedToken(token []byte) bool {
	return bytes.HasPrefix(token, []byte("github_pat_")) &&
		len(token) <= 1024 &&
		!strings.ContainsAny(string(token), " \t\r\n\x00")
}
