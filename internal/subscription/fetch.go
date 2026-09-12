package subscription

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type HTTPFetcher struct {
	client   *http.Client
	maxBytes int64
}

func NewHTTPFetcher(client *http.Client, maxBytes int64) *HTTPFetcher {
	if client == nil {
		client = http.DefaultClient
	}
	checkedClient := *client
	previousRedirect := checkedClient.CheckRedirect
	checkedClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" {
			return errors.New("subscription redirect must use HTTPS")
		}
		if previousRedirect != nil {
			return previousRedirect(request, via)
		}
		if len(via) >= 10 {
			return errors.New("too many subscription redirects")
		}
		return nil
	}
	return &HTTPFetcher{client: &checkedClient, maxBytes: maxBytes}
}

func (fetcher *HTTPFetcher) Fetch(ctx context.Context, source Source) ([]byte, string, error) {
	parsed, err := url.Parse(source.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, "", fmt.Errorf("source %s must be an absolute HTTPS URL", source.ID)
	}
	if fetcher.maxBytes <= 0 {
		return nil, "", errors.New("subscription response limit must be positive")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("build request for source %s: %w", source.ID, err)
	}
	request.Header.Set("User-Agent", "sing-box")
	request.Header.Set("Accept", "application/json, application/yaml, text/yaml, text/plain;q=0.9")

	response, err := fetcher.client.Do(request)
	if err != nil {
		return nil, "", sourceFetchError{id: source.ID, cause: err}
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("source %s returned HTTP %d", source.ID, response.StatusCode)
	}
	if response.ContentLength > fetcher.maxBytes {
		return nil, "", fmt.Errorf("source %s response exceeds byte limit", source.ID)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, fetcher.maxBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read source %s response: %w", source.ID, err)
	}
	if int64(len(body)) > fetcher.maxBytes {
		return nil, "", fmt.Errorf("source %s response exceeds byte limit", source.ID)
	}

	hint := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	return body, hint, nil
}

type sourceFetchError struct {
	id    string
	cause error
}

func (err sourceFetchError) Error() string { return "source " + err.id + " fetch failed" }
func (err sourceFetchError) Unwrap() error { return err.cause }
