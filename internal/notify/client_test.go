package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// Explicit opt-in only: this test sends one real operator notification.
func TestLiveNotification(t *testing.T) {
	if os.Getenv("GATEWAY_LIVE_PUSH_TEST") != "1" {
		t.Skip("requires explicit live push opt-in")
	}
	c, err := NewClient(os.Getenv("PUSH_BASE_URL"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Send(context.Background(), "代理网关提醒测试", "这是一条测试通知，用于验证推送通道。提醒项目：订阅刷新失败、可用节点少于30个、到期前7天续费。本条不代表实际故障或到期。"); err != nil {
		t.Fatal(err)
	}
}

func TestClientSendsEscapedTitleAndMessage(t *testing.T) {
	var escapedPath string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		escapedPath = request.URL.EscapedPath()
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := NewClient(server.URL+"/private-token", server.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	title := "代理订阅异常"
	message := "订阅 2（example.com）失败 / 使用缓存"
	if err := client.Send(context.Background(), title, message); err != nil {
		t.Fatalf("Send: %v", err)
	}
	want := "/private-token/" + url.PathEscape(title) + "/" + url.PathEscape(message)
	if escapedPath != want {
		t.Fatalf("escaped path = %q, want %q", escapedPath, want)
	}
}

func TestClientRejectsNonSuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "failed", http.StatusBadGateway)
	}))
	defer server.Close()

	client, err := NewClient(server.URL+"/private-token", server.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.Send(context.Background(), "title", "message"); err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("expected safe HTTP status error, got %v", err)
	}
}

func TestClientRejectsApplicationError(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":400,"message":"private details"}`))
	}))
	defer s.Close()
	c, err := NewClient(s.URL+"/token", s.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Send(context.Background(), "test", "test"); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("application failure not handled safely")
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var followed atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		followed.Store(true)
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	client, err := NewClient(redirector.URL+"/private-token", redirector.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.Send(context.Background(), "title", "message"); err == nil {
		t.Fatal("expected redirect response to fail")
	}
	if followed.Load() {
		t.Fatal("notification client followed a redirect")
	}
}
