package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNotificationsIncludeMachineIP(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "[192.0.2.10] 代理订阅异常") || !strings.Contains(r.URL.Path, "本机 IP：192.0.2.10\n订阅 1 失败") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(204)
	}))
	defer s.Close()
	c, err := NewClient(s.URL+"/test", s.Client(), "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Send(context.Background(), "代理订阅异常", "订阅 1 失败"); err != nil {
		t.Fatal(err)
	}
}
