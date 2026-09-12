package subscription

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPOptInAllowsOnlyExactSourceAndSameOriginRedirect(t *testing.T) {
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed cross-origin redirect") }))
	defer dest.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", 302)
			return
		}
		if r.URL.Path == "/cross" {
			http.Redirect(w, r, dest.URL, 302)
			return
		}
		if r.Header.Get("Referer") != "" {
			t.Error("leaked referer")
		}
		w.Write([]byte("http://node.invalid:8080"))
	}))
	defer s.Close()
	f := NewHTTPFetcher(s.Client(), 1024, s.URL+"/start", s.URL+"/cross")
	for _, tc := range []struct {
		path string
		ok   bool
	}{{"/start", true}, {"/final", false}, {"/cross", false}} {
		_, _, err := f.Fetch(context.Background(), Source{ID: "4", URL: s.URL + tc.path})
		if (err == nil) != tc.ok {
			t.Fatalf("path=%s err=%v", tc.path, err)
		}
	}
}
