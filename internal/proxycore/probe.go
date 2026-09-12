package proxycore

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"dual-egress-gateway/internal/pool"
)

type Prober struct {
	target         *url.URL
	expectedStatus int
	tlsConfig      *tls.Config
}

func NewProber(target string, expectedStatus int, tlsConfig *tls.Config) (*Prober, error) {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return nil, errors.New("probe target must be an absolute HTTPS URL")
	}
	if expectedStatus < 100 || expectedStatus > 599 {
		return nil, errors.New("expected probe status is invalid")
	}
	if tlsConfig == nil {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	if tlsConfig.InsecureSkipVerify {
		return nil, errors.New("probe TLS verification cannot be disabled")
	}
	clonedTLS := tlsConfig.Clone()
	clonedTLS.MinVersion = max(clonedTLS.MinVersion, tls.VersionTLS12)
	return &Prober{target: parsed, expectedStatus: expectedStatus, tlsConfig: clonedTLS}, nil
}

func (prober *Prober) Probe(ctx context.Context, dialer pool.Dialer) (time.Duration, error) {
	port := prober.target.Port()
	if port == "" {
		port = "443"
	}
	address := net.JoinHostPort(prober.target.Hostname(), port)
	started := time.Now()
	rawConn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return 0, err
	}
	defer rawConn.Close()

	tlsConfig := prober.tlsConfig.Clone()
	if tlsConfig.ServerName == "" {
		tlsConfig.ServerName = prober.target.Hostname()
	}
	tlsConn := tls.Client(rawConn, tlsConfig)
	if deadline, ok := ctx.Deadline(); ok {
		_ = tlsConn.SetDeadline(deadline)
	}
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return 0, err
	}

	request := &http.Request{
		Method: http.MethodGet,
		URL:    prober.target,
		Host:   prober.target.Host,
		Header: http.Header{"User-Agent": []string{"dual-egress-gateway/health"}},
	}
	request.Header.Set("Connection", "close")
	if err := request.Write(tlsConn); err != nil {
		return 0, err
	}
	response, err := http.ReadResponse(bufio.NewReader(tlsConn), request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != prober.expectedStatus {
		return 0, fmt.Errorf("unexpected HTTP status %s, want %s", strconv.Itoa(response.StatusCode), strconv.Itoa(prober.expectedStatus))
	}
	return time.Since(started), nil
}
