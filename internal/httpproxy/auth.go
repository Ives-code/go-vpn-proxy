package httpproxy

import (
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

func (server *Server) authorized(request *http.Request) bool {
	header := request.Header.Get("Proxy-Authorization")
	if !strings.HasPrefix(header, "Basic ") {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(header, "Basic ")))
	if err != nil {
		return false
	}
	wanted := []byte(server.config.Username + ":" + server.config.Password)
	return subtle.ConstantTimeCompare(decoded, wanted) == 1
}
