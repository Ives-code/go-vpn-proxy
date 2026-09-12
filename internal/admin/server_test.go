package admin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"dual-egress-gateway/internal/pool"
)

type fakeProvider struct {
	status    Status
	refreshes atomic.Int32
}

func (provider *fakeProvider) Status() Status { return provider.status }
func (provider *fakeProvider) Refresh(context.Context) error {
	provider.refreshes.Add(1)
	return nil
}

func TestStatusRedactsDefensively(t *testing.T) {
	provider := &fakeProvider{status: Status{
		Ready:        true,
		Nodes:        pool.Stats{Total: 2, Healthy: 1, Unhealthy: 1},
		SourceErrors: map[string]string{"2": "https://secret.invalid/list?token=do-not-leak failed"},
	}}
	handler := NewHandler(provider, "admin-token", []string{"do-not-leak"})
	request := httptest.NewRequest(http.MethodGet, "/status", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	body, _ := io.ReadAll(response.Result().Body)
	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d", response.Code)
	}
	for _, forbidden := range []string{"secret.invalid", "do-not-leak"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("status leaked %q: %s", forbidden, body)
		}
	}
}

func TestHealthReflectsReadiness(t *testing.T) {
	provider := &fakeProvider{status: Status{Ready: false}}
	handler := NewHandler(provider, "admin-token", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready status = %d", response.Code)
	}
	provider.status.Ready = true
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("ready status = %d", response.Code)
	}
}

func TestRefreshRequiresBearerToken(t *testing.T) {
	provider := &fakeProvider{}
	handler := NewHandler(provider, "admin-token", nil)
	request := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || provider.refreshes.Load() != 0 {
		t.Fatalf("unauthorized response=%d refreshes=%d", response.Code, provider.refreshes.Load())
	}

	request = httptest.NewRequest(http.MethodPost, "/refresh", nil)
	request.Header.Set("Authorization", "Bearer admin-token")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || provider.refreshes.Load() != 1 {
		t.Fatalf("authorized response=%d refreshes=%d", response.Code, provider.refreshes.Load())
	}
}
