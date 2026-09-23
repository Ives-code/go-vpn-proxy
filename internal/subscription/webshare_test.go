package subscription

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestOperatorWebshareFileWhenProvided(t *testing.T) {
	path := os.Getenv("WEBSHARE_TEST_FILE")
	if path == "" {
		t.Skip("only run with an explicit private input file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("private input is unavailable")
	}
	nodes, err := Parse(body, "text/plain")
	if err != nil {
		t.Fatalf("private input failed validation: %v", err)
	}
	if len(nodes) != 100 {
		t.Fatalf("private input has %d valid records, expected 100", len(nodes))
	}
}

func TestParseWebshareHTTPProxies(t *testing.T) {
	body := []byte("192.0.2.1:8080:user-one:secret-one\n192.0.2.2:8081:user-two:secret-two\n")
	nodes, err := Parse(body, "text/plain")
	if err != nil || len(nodes) != 2 {
		t.Fatalf("nodes=%d err=%v", len(nodes), err)
	}
	var options map[string]any
	if err := json.Unmarshal(nodes[0].Options, &options); err != nil {
		t.Fatal(err)
	}
	if nodes[0].Type != "http" || nodes[0].Format != FormatSingBox || options["server"] != "192.0.2.1" || options["server_port"] != float64(8080) || options["username"] != "user-one" || options["password"] != "secret-one" {
		t.Fatalf("incorrect HTTP outbound normalization: %v", options)
	}
	if nodes[0].ID == nodes[1].ID || strings.Contains(nodes[0].ID, "secret") {
		t.Fatal("IDs collide or disclose credentials")
	}
}

func TestParseWebshareRejectsMalformedRecordAtomically(t *testing.T) {
	for _, bad := range []string{
		"192.0.2.2:0:user:secret",
		"192.0.2.2:8080:user:",
		"not-an-ip:8080:user:secret",
		"192.0.2.2:8080:user:secret:extra",
	} {
		_, err := Parse([]byte("192.0.2.1:8080:user:secret\n"+bad), "text/plain")
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("bad record not safely rejected: %v", err)
		}
	}
}
