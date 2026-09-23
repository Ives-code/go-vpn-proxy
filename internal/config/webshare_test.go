package config

import "testing"

func TestWebshareFileConfigRequiresAbsolutePath(t *testing.T) {
	for _, tc := range []struct {
		yaml  string
		valid bool
	}{
		{"webshare_file: /etc/dual-egress-gateway/webshare-proxies.txt\n", true},
		{"webshare_file: webshare-proxies.txt\n", false},
	} {
		cfg, err := Load(writeConfig(t, tc.yaml), lookup(validEnvironment()))
		if (err == nil) != tc.valid {
			t.Fatalf("config valid=%t err=%v", tc.valid, err)
		}
		if tc.valid && cfg.WebshareFile == "" {
			t.Fatal("file path was not preserved")
		}
	}
}
