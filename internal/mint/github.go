package mint

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const (
	apiURL     = "https://api.github.com"
	apiVersion = "2026-03-10"
)

type githubClient struct {
	http *http.Client
	base string
}

func newGitHubClient() *githubClient {
	return &githubClient{
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
		base: apiURL,
	}
}

type githubAPIError struct {
	method string
	path   string
	status int
}

func (e *githubAPIError) Error() string {
	return fmt.Sprintf("GitHub API returned HTTP %d for %s %s", e.status, e.method, e.path)
}

func (g *githubClient) validateRepositoryToken(ctx context.Context, repo repository, token string) error {
	path := "/repos/" + url.PathEscape(repo.Owner) + "/" + url.PathEscape(repo.Name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.base+path, nil)
	if err != nil {
		return errors.New("cannot construct GitHub API request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", "mint")
	client := *g.http
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := client.Do(req)
	if err != nil {
		return errors.New("GitHub API request failed; check connectivity and TLS configuration")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return &githubAPIError{status: response.StatusCode, method: http.MethodGet, path: path}
	}
	return nil
}
