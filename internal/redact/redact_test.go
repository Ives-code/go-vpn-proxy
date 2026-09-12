package redact

import (
	"strings"
	"testing"
)

func TestTextRemovesSecretsAndEndpoints(t *testing.T) {
	input := "fetch https://user:pass@subscription.invalid/path?token=topsecret failed via 192.0.2.10:443 using topsecret"
	got := Text(input, "topsecret", "user", "pass")
	for _, forbidden := range []string{"topsecret", "user", "pass", "subscription.invalid", "192.0.2.10"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("redacted text still contains %q: %q", forbidden, got)
		}
	}
}
