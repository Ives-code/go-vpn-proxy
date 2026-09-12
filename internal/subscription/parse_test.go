package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestParseSingBoxJSON(t *testing.T) {
	body := []byte(`{"outbounds":[{"type":"direct","tag":"direct"},{"type":"vless","tag":"friendly-name","server":"edge.invalid","server_port":443,"uuid":"example-uuid"}]}`)

	nodes, err := Parse(body, "")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("node count = %d", len(nodes))
	}
	if nodes[0].Type != "vless" || nodes[0].Format != FormatSingBox {
		t.Fatalf("unexpected node: %#v", nodes[0])
	}
	if nodes[0].ID == "" || nodes[0].ID == "friendly-name" {
		t.Fatalf("unsafe or empty stable ID: %q", nodes[0].ID)
	}
}

func TestParseClashYAML(t *testing.T) {
	body := []byte("proxies:\n  - name: friendly-name\n    type: vless\n    server: edge.invalid\n    port: 443\n    uuid: example-uuid\n")

	nodes, err := Parse(body, "")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Format != FormatSingBox {
		t.Fatalf("unexpected nodes: %#v", nodes)
	}
	var options map[string]any
	if err := json.Unmarshal(nodes[0].Options, &options); err != nil {
		t.Fatalf("unmarshal options: %v", err)
	}
	if options["server_port"] != float64(443) {
		t.Fatalf("normalized server_port = %#v", options["server_port"])
	}
}

func TestParseURILists(t *testing.T) {
	plain := "vless://example-uuid@edge.invalid:443?security=tls&type=ws&path=%2Fsocket&sni=edge.invalid#friendly-name\nhttp://user:pass@other.invalid:8080#other\n"
	for name, body := range map[string][]byte{
		"plain":  []byte(plain),
		"base64": []byte(base64.StdEncoding.EncodeToString([]byte(plain))),
	} {
		t.Run(name, func(t *testing.T) {
			nodes, err := Parse(body, "")
			if err != nil {
				t.Fatalf("Parse returned error: %v", err)
			}
			if len(nodes) != 2 {
				t.Fatalf("node count = %d", len(nodes))
			}
			for _, node := range nodes {
				if node.Format != FormatSingBox || node.ID == "" {
					t.Fatalf("unexpected node: %#v", node)
				}
			}
		})
	}
}

func TestParseRejectsMixedUnsupportedProtocols(t *testing.T) {
	body := []byte("trojan://password@unsupported.invalid:443\nvless://id@supported.invalid:443\n")
	_, err := Parse(body, "")
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported-protocol error, got %v", err)
	}
}

func TestParseRejectsSourceWithOnlyUnsupportedProtocols(t *testing.T) {
	_, err := Parse([]byte("trojan://password@unsupported.invalid:443\n"), "")
	if err == nil || !strings.Contains(err.Error(), "supported") {
		t.Fatalf("expected supported-protocol error, got %v", err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(`{"outbounds":[]}`))
	f.Add([]byte("proxies: []\n"))
	f.Add([]byte("vless://id@example.invalid:443\n"))
	f.Fuzz(func(t *testing.T, body []byte) {
		_, _ = Parse(body, "")
	})
}

func TestParseRejectsExcessiveNodeCount(t *testing.T) {
	var body strings.Builder
	for index := 0; index < MaxNodesPerSource+1; index++ {
		_, _ = fmt.Fprintf(&body, "vless://id-%d@edge.invalid:443\n", index)
	}
	_, err := Parse([]byte(body.String()), "")
	if err == nil || !strings.Contains(err.Error(), "node limit") {
		t.Fatalf("expected node limit error, got %v", err)
	}
}

func TestParseRejectsExcessiveStructuralComplexity(t *testing.T) {
	nested := `"value"`
	for index := 0; index < MaxStructureDepth+1; index++ {
		nested = `{"nested":` + nested + `}`
	}
	body := []byte(`{"outbounds":[{"type":"vless","tag":"node","server":"edge.invalid","server_port":443,"uuid":"id","extra":` + nested + `}]}`)
	_, err := Parse(body, "")
	if err == nil || !strings.Contains(err.Error(), "complex") {
		t.Fatalf("expected complexity error, got %v", err)
	}
}

func TestParseRejectsUnknownVLESSSecurityAndTransport(t *testing.T) {
	for name, body := range map[string][]byte{
		"unknown-security":   []byte("vless://id@edge.invalid:443?security=unknown\n"),
		"unknown-transport":  []byte("vless://id@edge.invalid:443?type=quic\n"),
		"clash-transport":    []byte("proxies:\n  - name: node\n    type: vless\n    server: edge.invalid\n    port: 443\n    uuid: id\n    network: quic\n"),
		"incomplete-reality": []byte("vless://id@edge.invalid:443?security=reality&sni=edge.invalid\n"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(body, "")
			if err == nil || !strings.Contains(err.Error(), "unsupported") {
				t.Fatalf("expected fail-closed normalization error, got %v", err)
			}
		})
	}
}
