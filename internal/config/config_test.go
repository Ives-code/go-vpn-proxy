package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func lookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func validEnvironment() map[string]string {
	return map[string]string{
		"PROXY_USERNAME":    "lan-user",
		"PROXY_PASSWORD":    "correct horse battery staple",
		"ADMIN_TOKEN":       "local-admin-token",
		"SUBSCRIPTION_URLS": "https://subscription.invalid/one\nhttps://subscription.invalid/two",
	}
}

func TestLoadAppliesSafeDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, "{}\n"), lookup(validEnvironment()))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.HTTPListen != "127.0.0.1:18080" {
		t.Fatalf("HTTPListen = %q", cfg.HTTPListen)
	}
	if cfg.WSListen != "127.0.0.1:18081" {
		t.Fatalf("WSListen = %q", cfg.WSListen)
	}
	if cfg.AdminListen != "127.0.0.1:19090" {
		t.Fatalf("AdminListen = %q", cfg.AdminListen)
	}
	if len(cfg.SubscriptionURLs) != 2 {
		t.Fatalf("SubscriptionURLs count = %d", len(cfg.SubscriptionURLs))
	}
	if cfg.LowNodeThreshold != 30 {
		t.Fatalf("LowNodeThreshold = %d", cfg.LowNodeThreshold)
	}
	if cfg.PushBaseURL.Reveal() != "" {
		t.Fatal("PushBaseURL must be empty by default")
	}
}

func TestLoadAcceptsPushNotifications(t *testing.T) {
	environment := validEnvironment()
	environment["PUSH_BASE_URL"] = "http://push.invalid/private-token"
	path := writeConfig(t, "low_node_threshold: 12\n")

	cfg, err := Load(path, lookup(environment))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.PushBaseURL.Reveal() != environment["PUSH_BASE_URL"] {
		t.Fatal("PushBaseURL was not loaded")
	}
	if cfg.LowNodeThreshold != 12 {
		t.Fatalf("LowNodeThreshold = %d", cfg.LowNodeThreshold)
	}
}

func TestLoadRejectsInvalidPushConfiguration(t *testing.T) {
	tests := []struct {
		name       string
		pushURL    string
		configBody string
		errorText  string
	}{
		{name: "scheme", pushURL: "ftp://push.invalid/token", configBody: "{}\n", errorText: "PUSH_BASE_URL"},
		{name: "query", pushURL: "https://push.invalid/token?leak=yes", configBody: "{}\n", errorText: "PUSH_BASE_URL"},
		{name: "threshold", configBody: "low_node_threshold: -1\n", errorText: "low_node_threshold"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			environment := validEnvironment()
			if test.pushURL != "" {
				environment["PUSH_BASE_URL"] = test.pushURL
			}
			_, err := Load(writeConfig(t, test.configBody), lookup(environment))
			if err == nil || !strings.Contains(err.Error(), test.errorText) {
				t.Fatalf("expected %q error, got %v", test.errorText, err)
			}
		})
	}
}

func TestLoadRejectsMissingSecrets(t *testing.T) {
	environment := validEnvironment()
	delete(environment, "PROXY_PASSWORD")

	_, err := Load(writeConfig(t, "{}\n"), lookup(environment))
	if err == nil || !strings.Contains(err.Error(), "PROXY_PASSWORD") {
		t.Fatalf("expected missing PROXY_PASSWORD error, got %v", err)
	}
}

func TestLoadRejectsDuplicateListeners(t *testing.T) {
	path := writeConfig(t, "http_listen: 127.0.0.1:18080\nws_listen: 127.0.0.1:18080\n")

	_, err := Load(path, lookup(validEnvironment()))
	if err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("expected duplicate listener error, got %v", err)
	}
}

func TestLoadRejectsNonLoopbackAdmin(t *testing.T) {
	path := writeConfig(t, "admin_listen: 0.0.0.0:19090\n")

	_, err := Load(path, lookup(validEnvironment()))
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("expected loopback error, got %v", err)
	}
}

func TestSecretFormattingIsRedacted(t *testing.T) {
	secret := Secret("do-not-print-me")
	for _, formatted := range []string{fmt.Sprint(secret), fmt.Sprintf("%s", secret), fmt.Sprintf("%#v", secret)} {
		if formatted != "[REDACTED]" {
			t.Fatalf("secret formatted as %q", formatted)
		}
	}
}
