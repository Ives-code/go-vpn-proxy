package subscription

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type HTTPFetcher struct {
	client      *http.Client
	maxBytes    int64
	allowedHTTP map[string]bool
}

func NewHTTPFetcher(client *http.Client, maxBytes int64, httpAllowlist ...string) *HTTPFetcher {
	allowed := make(map[string]bool, len(httpAllowlist))
	for _, value := range httpAllowlist {
		allowed[value] = true
	}
	if client == nil {
		client = http.DefaultClient
	}
	checkedClient := *client
	checkedClient.Jar = nil
	previousRedirect := checkedClient.CheckRedirect
	checkedClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" && !(request.URL.Scheme == "http" && len(via) > 0 && allowed[via[0].URL.String()]) {
			return errors.New("subscription redirect must use HTTPS")
		}
		if len(via) > 0 && (request.URL.Scheme != via[0].URL.Scheme || request.URL.Host != via[0].URL.Host) {
			return errors.New("subscription redirect must remain on the original origin")
		}
		request.Header.Del("Referer")
		request.Header.Del("Cookie")
		request.Header.Del("Authorization")
		request.Header.Del("Proxy-Authorization")
		if previousRedirect != nil {
			return previousRedirect(request, via)
		}
		if len(via) >= 10 {
			return errors.New("too many subscription redirects")
		}
		return nil
	}
	return &HTTPFetcher{client: &checkedClient, maxBytes: maxBytes, allowedHTTP: allowed}
}

func (fetcher *HTTPFetcher) Fetch(ctx context.Context, source Source) ([]byte, string, error) {
	return fetcher.fetch(ctx, source, nil)
}

func (fetcher *HTTPFetcher) FetchWithMetadata(ctx context.Context, source Source) ([]byte, string, time.Time, error) {
	var expiry time.Time
	body, hint, err := fetcher.fetch(ctx, source, &expiry)
	return body, hint, expiry, err
}

func ParseExpiry(header string) time.Time {
	for _, field := range strings.Split(header, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "expire") {
			seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err == nil && seconds > 0 && seconds <= 253402300799 {
				return time.Unix(seconds, 0).UTC()
			}
		}
	}
	return time.Time{}
}

func (fetcher *HTTPFetcher) fetch(ctx context.Context, source Source, expiry *time.Time) ([]byte, string, error) {
	parsed, err := url.Parse(source.URL)
	if err != nil || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && fetcher.allowedHTTP[source.URL])) || parsed.Host == "" {
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
	if expiry != nil {
		*expiry = ParseExpiry(response.Header.Get("Subscription-Userinfo"))
	}
	return body, hint, nil
}

type sourceFetchError struct {
	id    string
	cause error
}

func (err sourceFetchError) Error() string { return "source " + err.id + " fetch failed" }
func (err sourceFetchError) Unwrap() error { return err.cause }
