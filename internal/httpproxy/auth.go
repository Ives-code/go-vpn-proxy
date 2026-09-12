package httpproxy

import (
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

func (server *Server) authorized(request *http.Request) bool {
	header := request.Header.Get("Proxy-Authorization")
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Basic") {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	wanted := []byte(server.config.Username + ":" + server.config.Password)
	return subtle.ConstantTimeCompare(decoded, wanted) == 1
}
