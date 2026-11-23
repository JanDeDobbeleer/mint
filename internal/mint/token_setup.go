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
	path := tokenFilePath(dir, repo)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return createRepositoryToken(ctx, client, path, repo, prompts, stderr, nil, false)
	}
	if err != nil {
		return "", errors.New("cannot inspect the saved repository token")
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("saved repository token must be a regular file")
	}

	password, err := prompts.password("Mint token-store passphrase: ")
	if err != nil {
		return "", err
	}
	defer clear(password)
	token, err := readEncryptedToken(path, repo, password)
	if err != nil {
		replace, promptErr := prompts.confirm("The saved token could not be unlocked. Replace it with a new token? Type yes to replace [no]: ")
		if promptErr != nil {
			return "", promptErr
		}
		if !replace {
			return "", errors.New("cannot unlock the saved repository token")
		}
		return createRepositoryToken(ctx, client, path, repo, prompts, stderr, nil, true)
	}
	defer clear(token)

	if !isFineGrainedToken(token) {
		if _, err := fmt.Fprintf(stderr, "The saved credential is not a fine-grained personal access token for %s/%s; create a replacement token.\n", repo.Owner, repo.Name); err != nil {
			return "", errors.New("cannot report repository token status")
		}
		return createRepositoryToken(ctx, client, path, repo, prompts, stderr, password, true)
	}
	err = client.validateRepositoryToken(ctx, repo, string(token))
	if err == nil {
		return string(token), nil
	}
	if !repositoryTokenNeedsReplacement(err) {
		return "", err
	}
	if _, err := fmt.Fprintf(stderr, "The saved token cannot access %s/%s; create a replacement token.\n", repo.Owner, repo.Name); err != nil {
		return "", errors.New("cannot report repository token status")
	}
	return createRepositoryToken(ctx, client, path, repo, prompts, stderr, password, true)
}

func repositoryTokenNeedsReplacement(err error) bool {
	var apiErr *githubAPIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.status == 401 || apiErr.status == 404
}

func createRepositoryToken(ctx context.Context, client *githubClient, path string, repo repository, prompts secretPrompter, stderr io.Writer, existingPassword []byte, replace bool) (string, error) {
	if _, err := fmt.Fprintf(stderr, "Create a fine-grained personal access token for %s/%s:\n%s\nSelect only this repository. The form pre-fills Contents, Issues, Pull requests, and Workflows write access; remove permissions you do not need.\n",
		repo.Owner, repo.Name, tokenCreationURL(repo)); err != nil {
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

	password := existingPassword
	if len(password) == 0 {
		password, err = prompts.password("New Mint token-store passphrase: ")
		if err != nil {
			return "", err
		}
		defer clear(password)
		if len(password) == 0 {
			return "", errors.New("token-store passphrase cannot be empty")
		}
		confirmation, promptErr := prompts.password("Confirm Mint token-store passphrase: ")
		if promptErr != nil {
			return "", promptErr
		}
		defer clear(confirmation)
		if !bytes.Equal(password, confirmation) {
			return "", errors.New("token-store passphrases do not match")
		}
	}
	if err := writeEncryptedToken(path, repo, token, password, replace); err != nil {
		return "", err
	}
	if _, err := fmt.Fprintf(stderr, "Saved encrypted token for %s/%s.\n", repo.Owner, repo.Name); err != nil {
		return "", errors.New("cannot report repository token status")
	}
	return string(token), nil
}

func isFineGrainedToken(token []byte) bool {
	return bytes.HasPrefix(token, []byte("github_pat_")) &&
		len(token) <= 1024 &&
		!strings.ContainsAny(string(token), " \t\r\n\x00")
}
