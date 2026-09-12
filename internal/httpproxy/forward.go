package httpproxy

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

var hopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

func (server *Server) handleForward(response http.ResponseWriter, request *http.Request) {
	if request.URL == nil || request.URL.Scheme == "" || request.URL.Host == "" {
		http.Error(response, "absolute proxy URL required", http.StatusBadRequest)
		return
	}
	if request.URL.Scheme != "http" {
		http.Error(response, "use CONNECT for HTTPS", http.StatusBadRequest)
		return
	}
	session := sessionFrom(request)
	if session == nil {
		http.Error(response, "connection state unavailable", http.StatusInternalServerError)
		return
	}

	outboundRequest := request.Clone(request.Context())
	outboundRequest.RequestURI = ""
	outboundRequest.Header = request.Header.Clone()
	upgradeProtocol := outboundRequest.Header.Get("Upgrade")
	wantsUpgrade := upgradeProtocol != "" && headerContainsToken(outboundRequest.Header.Get("Connection"), "upgrade")
	removeHopByHop(outboundRequest.Header)
	if wantsUpgrade {
		outboundRequest.Header.Set("Connection", "Upgrade")
		outboundRequest.Header.Set("Upgrade", upgradeProtocol)
	}

	transport := session.httpTransport()
	upstreamResponse, err := transport.RoundTrip(outboundRequest)
	if err != nil {
		http.Error(response, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer upstreamResponse.Body.Close()
	if wantsUpgrade && upstreamResponse.StatusCode == http.StatusSwitchingProtocols {
		server.handleUpgrade(response, request, session, upstreamResponse)
		return
	}

	copyHeaders(response.Header(), upstreamResponse.Header)
	removeHopByHop(response.Header())
	response.WriteHeader(upstreamResponse.StatusCode)
	_, _ = io.Copy(response, upstreamResponse.Body)
}

func (server *Server) handleUpgrade(response http.ResponseWriter, request *http.Request, session *connectionSession, upstreamResponse *http.Response) {
	upstream, ok := upstreamResponse.Body.(io.ReadWriteCloser)
	if !ok {
		http.Error(response, "upstream upgrade unavailable", http.StatusBadGateway)
		return
	}
	hijacker, ok := response.(http.Hijacker)
	if !ok {
		http.Error(response, "client upgrade unavailable", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer func() {
		_ = client.Close()
		_ = upstream.Close()
		session.release()
		server.sessions.Delete(session.client)
	}()

	if _, err := fmt.Fprintf(client, "HTTP/1.1 101 Switching Protocols\r\n"); err != nil {
		return
	}
	copyHeadersForUpgrade(client, upstreamResponse.Header)
	if _, err := client.Write([]byte("\r\n")); err != nil {
		return
	}
	if buffered.Reader.Buffered() > 0 {
		if _, err := io.CopyN(upstream, buffered, int64(buffered.Reader.Buffered())); err != nil {
			return
		}
	}

	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		_, _ = io.Copy(upstream, client)
		if closeWriter, ok := upstream.(interface{ CloseWrite() error }); ok {
			_ = closeWriter.CloseWrite()
		}
	}()
	go func() {
		defer wait.Done()
		_, _ = io.Copy(client, upstream)
		if closeWriter, ok := client.(interface{ CloseWrite() error }); ok {
			_ = closeWriter.CloseWrite()
		}
	}()
	wait.Wait()
}

func copyHeadersForUpgrade(destination net.Conn, source http.Header) {
	for name, values := range source {
		for _, value := range values {
			_, _ = fmt.Fprintf(destination, "%s: %s\r\n", name, value)
		}
	}
}

func headerContainsToken(value, wanted string) bool {
	for _, token := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(token), wanted) {
			return true
		}
	}
	return false
}

func (session *connectionSession) httpTransport() *http.Transport {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.transport == nil {
		session.transport = &http.Transport{
			Proxy:               nil,
			DialContext:         session.dial,
			ForceAttemptHTTP2:   false,
			MaxIdleConns:        1,
			MaxIdleConnsPerHost: 1,
			IdleConnTimeout:     session.server.http.IdleTimeout,
		}
	}
	return session.transport
}

func removeHopByHop(header http.Header) {
	for _, token := range strings.Split(header.Get("Connection"), ",") {
		if token = strings.TrimSpace(token); token != "" {
			header.Del(token)
		}
	}
	for _, name := range hopByHopHeaders {
		header.Del(name)
	}
}

func copyHeaders(destination, source http.Header) {
	for name, values := range source {
		for _, value := range values {
			destination.Add(name, value)
		}
	}
}
