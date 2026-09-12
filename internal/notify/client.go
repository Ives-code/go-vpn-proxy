package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxResponseBytes = 4 << 10

type Client struct {
	baseURL string
	http    *http.Client
	hostIP  string
}

func NewClient(baseURL string, httpClient *http.Client, hostIPs ...string) (*Client, error) {
	hostIP := ""
	if len(hostIPs) > 0 {
		hostIP = hostIPs[0]
		if hostIP == "" {
			hostIP = localIP()
		}
		if net.ParseIP(hostIP) == nil {
			return nil, errors.New("notification host must be an IP address")
		}
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("notification base URL must be an absolute HTTP or HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("notification base URL must not contain user information, a query, or a fragment")
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	clientCopy := *httpClient
	if clientCopy.Timeout <= 0 {
		clientCopy.Timeout = 10 * time.Second
	}
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{baseURL: strings.TrimRight(parsed.String(), "/"), http: &clientCopy, hostIP: hostIP}, nil
}

func (client *Client) Send(ctx context.Context, title, message string) error {
	if client.hostIP != "" {
		title = "[" + client.hostIP + "] " + title
		message = "本机 IP：" + client.hostIP + "\n" + message
	}
	endpoint := client.baseURL + "/" + url.PathEscape(title) + "/" + url.PathEscape(message)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.New("build notification request")
	}
	response, err := client.http.Do(request)
	if err != nil {
		return errors.New("notification request failed")
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("notification returned HTTP %d", response.StatusCode)
	}
	if readErr != nil || len(body) > maxResponseBytes {
		return errors.New("invalid notification response")
	}
	if len(strings.TrimSpace(string(body))) > 0 {
		var result struct {
			Code *int `json:"code"`
		}
		if strings.Contains(response.Header.Get("Content-Type"), "json") || strings.HasPrefix(strings.TrimSpace(string(body)), "{") {
			if json.Unmarshal(body, &result) != nil {
				return errors.New("invalid notification response")
			}
			if result.Code != nil && *result.Code != 200 {
				return errors.New("notification service rejected message")
			}
		}
	}
	return nil
}

func localIP() string {
	addresses, _ := net.InterfaceAddrs()
	for _, address := range addresses {
		if n, ok := address.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			return n.IP.String()
		}
	}
	return "127.0.0.1"
}
