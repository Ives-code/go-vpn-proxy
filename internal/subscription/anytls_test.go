package subscription

import (
	"encoding/json"
	"testing"
)

func TestClashAnyTLSNormalization(t *testing.T) {
	nodes, err := Parse([]byte(`proxies:
  - name: test
    type: anytls
    server: example.invalid
    port: 443
    password: test-only
    sni: tls.example.invalid
    client-fingerprint: chrome
    skip-cert-verify: false
    idle-session-check-interval: 30
    idle-session-timeout: 60
    min-idle-session: 2
`), "application/yaml")
	if err != nil || len(nodes) != 1 {
		t.Fatalf("nodes=%d err=%v", len(nodes), err)
	}
	var o map[string]any
	if err = json.Unmarshal(nodes[0].Options, &o); err != nil {
		t.Fatal(err)
	}
	tls := o["tls"].(map[string]any)
	if o["type"] != "anytls" || o["password"] != "test-only" || o["idle_session_timeout"] != "60s" || o["min_idle_session"] != float64(2) || tls["enabled"] != true || tls["server_name"] != "tls.example.invalid" {
		t.Fatalf("unexpected normalization: %v", o)
	}
}

func TestClashAnyTLSRejectsInvalidSettings(t *testing.T) {
	for _, field := range []string{"password: ''", "password: test\n    idle-session-timeout: invalid", "password: test\n    min-idle-session: -1"} {
		_, err := Parse([]byte("proxies:\n  - type: anytls\n    server: example.invalid\n    port: 443\n    "+field+"\n"), "application/yaml")
		if err == nil {
			t.Fatalf("accepted invalid field %s", field)
		}
	}
}
