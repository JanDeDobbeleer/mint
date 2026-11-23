package mint

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func testHTTPResponse(request *http.Request, status int, headers http.Header, body string) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func TestRepositoryTokenValidation(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			client := &githubClient{
				base: "https://api.github.test",
				http: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
					assert.Equal(t, http.MethodGet, request.Method)
					assert.Equal(t, "/repos/owner/project", request.URL.Path)
					assert.Equal(t, "Bearer repository-pat", request.Header.Get("Authorization"))
					return testHTTPResponse(request, status, nil, ""), nil
				})},
			}

			err := client.validateRepositoryToken(context.Background(), repository{Owner: "owner", Name: "project"}, "repository-pat")
			if status == http.StatusOK {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, fmt.Sprintf("HTTP %d", status))
			assert.NotContains(t, err.Error(), "repository-pat")
		})
	}
}

func TestGitHubClientDoesNotForwardCredentialsOnRedirect(t *testing.T) {
	tests := []struct {
		name string
		code int
	}{
		{name: "permanent", code: http.StatusMovedPermanently},
		{name: "temporary", code: http.StatusTemporaryRedirect},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var leaked atomic.Bool
			var requests atomic.Int32
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				requests.Add(1)
				if request.URL.Host == "target.github.test" {
					leaked.Store(request.Header.Get("Authorization") != "")
					return testHTTPResponse(request, http.StatusNoContent, nil, ""), nil
				}
				return testHTTPResponse(request, tt.code, http.Header{
					"Location": []string{"https://target.github.test/"},
				}, ""), nil
			})
			client := &githubClient{
				base: "https://api.github.test",
				http: &http.Client{Transport: transport},
			}
			err := client.validateRepositoryToken(context.Background(), repository{Owner: "owner", Name: "project"}, "private-test-token")
			require.ErrorContains(t, err, fmt.Sprintf("HTTP %d", tt.code))
			assert.NotContains(t, err.Error(), "private-test-token")
			assert.False(t, leaked.Load())
			assert.EqualValues(t, 1, requests.Load())
		})
	}
}
