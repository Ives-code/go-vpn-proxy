package admin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"dual-egress-gateway/internal/pool"
	"dual-egress-gateway/internal/redact"
)

type Status struct {
	Ready        bool              `json:"ready"`
	Nodes        pool.Stats        `json:"nodes"`
	HTTPActive   int64             `json:"http_active_connections"`
	WSActive     int64             `json:"ws_active_connections"`
	LastRefresh  time.Time         `json:"last_refresh,omitempty"`
	SourceErrors map[string]string `json:"source_errors,omitempty"`
}

type Provider interface {
	Status() Status
	Refresh(context.Context) error
}

type Handler struct {
	provider Provider
	token    string
	secrets  []string
	mux      *http.ServeMux
}

func NewHandler(provider Provider, token string, secrets []string) *Handler {
	handler := &Handler{provider: provider, token: token, secrets: append([]string(nil), secrets...), mux: http.NewServeMux()}
	handler.mux.HandleFunc("GET /healthz", handler.health)
	handler.mux.HandleFunc("GET /status", handler.status)
	handler.mux.HandleFunc("POST /refresh", handler.refresh)
	return handler
}

func (handler *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	handler.mux.ServeHTTP(response, request)
}

func (handler *Handler) health(response http.ResponseWriter, _ *http.Request) {
	status := handler.provider.Status()
	response.Header().Set("Content-Type", "application/json")
	if !status.Ready {
		response.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(response).Encode(map[string]bool{"ready": status.Ready})
}

func (handler *Handler) status(response http.ResponseWriter, _ *http.Request) {
	status := handler.provider.Status()
	status.SourceErrors = cloneAndRedact(status.SourceErrors, handler.secrets)
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(status)
}

func (handler *Handler) refresh(response http.ResponseWriter, request *http.Request) {
	provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(provided), []byte(handler.token)) != 1 {
		http.Error(response, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := handler.provider.Refresh(request.Context()); err != nil {
		http.Error(response, "refresh failed", http.StatusBadGateway)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func cloneAndRedact(input map[string]string, secrets []string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	output := make(map[string]string, len(input))
	for sourceID, message := range input {
		output[sourceID] = redact.Text(message, secrets...)
	}
	return output
}
