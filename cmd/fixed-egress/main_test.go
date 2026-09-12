package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFixedProxyRequiresExactlyOneNode(t *testing.T) {
	for _, tc := range []struct {
		body string
		ok   bool
	}{
		{`{"outbounds":[{"type":"http","server":"one.invalid","server_port":8080}]}`, true},
		{`{"outbounds":[]}`, false},
		{`{"outbounds":[{"type":"http","server":"one.invalid","server_port":8080},{"type":"http","server":"two.invalid","server_port":8080}]}`, false},
	} {
		p := filepath.Join(t.TempDir(), "node.json")
		if err := os.WriteFile(p, []byte(tc.body), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := singleNode(p)
		if (err == nil) != tc.ok {
			t.Fatalf("single-node validation err=%v", err)
		}
	}
}
