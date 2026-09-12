package subscription

import (
	"encoding/base64"
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
	if len(nodes) != 1 || nodes[0].Format != FormatClash {
		t.Fatalf("unexpected nodes: %#v", nodes)
	}
}

func TestParseURILists(t *testing.T) {
	plain := "vless://example-uuid@edge.invalid:443?security=tls#friendly-name\ntrojan://password@other.invalid:443#other\n"
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
				if node.Format != FormatURI || node.ID == "" {
					t.Fatalf("unexpected node: %#v", node)
				}
			}
		})
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
