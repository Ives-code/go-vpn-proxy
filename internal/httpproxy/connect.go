package httpproxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

func (server *Server) handleConnect(response http.ResponseWriter, request *http.Request) {
	session := sessionFrom(request)
	if session == nil {
		http.Error(response, "connection state unavailable", http.StatusInternalServerError)
		return
	}
	target := request.Host
	if !strings.Contains(target, ":") {
		target += ":443"
	}
	upstream, err := session.dial(request.Context(), "tcp", target)
	if err != nil {
		http.Error(response, "upstream unavailable", http.StatusBadGateway)
		return
	}

	hijacker, ok := response.(http.Hijacker)
	if !ok {
		upstream.Close()
		http.Error(response, "tunneling unavailable", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	defer func() {
		_ = client.Close()
		_ = upstream.Close()
		session.release()
		server.sessions.Delete(session.client)
	}()

	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	if buffered.Reader.Buffered() > 0 {
		if _, err := io.CopyN(upstream, buffered, int64(buffered.Reader.Buffered())); err != nil {
			return
		}
	}
	tunnel(request.Context(), client, upstream)
}

func tunnel(ctx context.Context, client net.Conn, upstream net.Conn) {
	var wait sync.WaitGroup
	wait.Add(2)
	copyOneWay := func(destination net.Conn, source io.Reader) {
		defer wait.Done()
		_, _ = io.Copy(destination, source)
		if closeWriter, ok := destination.(interface{ CloseWrite() error }); ok {
			_ = closeWriter.CloseWrite()
		}
	}
	go copyOneWay(upstream, client)
	go copyOneWay(client, upstream)
	stopWatcher := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = client.SetDeadline(timeNow())
			_ = upstream.SetDeadline(timeNow())
		case <-stopWatcher:
		}
	}()
	wait.Wait()
	close(stopWatcher)
}

var timeNow = func() time.Time { return time.Now() }
