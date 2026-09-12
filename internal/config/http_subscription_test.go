package config

import "testing"

func TestHTTPSubscriptionRequiresExactAllowlist(t *testing.T) {
	for _, allowed := range []string{"", "http://example.invalid/other", "http://example.invalid/list"} {
		env := validEnvironment()
		env["SUBSCRIPTION_URLS"] = "http://example.invalid/list"
		env["HTTP_SUBSCRIPTION_ALLOWLIST"] = allowed
		_, err := Load(writeConfig(t, "{}"), lookup(env))
		if (err == nil) != (allowed == "http://example.invalid/list") {
			t.Fatalf("allowlist match=%v err=%v", allowed, err)
		}
	}
}
