package subscription

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var ignoredOutboundTypes = map[string]bool{
	"block": true, "direct": true, "dns": true, "selector": true, "urltest": true,
}

func Parse(body []byte, _ string) ([]NodeSpec, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, errors.New("subscription is empty")
	}

	if nodes, recognized, err := parseJSON(trimmed); recognized {
		return nodes, err
	}
	if nodes, recognized, err := parseClash(trimmed); recognized {
		return nodes, err
	}

	text := string(trimmed)
	if decoded, ok := decodeBase64(text); ok {
		text = decoded
	}
	return parseURIs(text)
}

func parseJSON(body []byte) ([]NodeSpec, bool, error) {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, false, nil
	}

	var entries []any
	switch value := root.(type) {
	case map[string]any:
		outbounds, ok := value["outbounds"].([]any)
		if !ok {
			return nil, true, errors.New("JSON subscription has no outbounds array")
		}
		entries = outbounds
	case []any:
		entries = value
	default:
		return nil, true, errors.New("JSON subscription root must be an object or array")
	}

	return nodesFromMaps(entries, FormatSingBox, "tag"), true, nil
}

func parseClash(body []byte) ([]NodeSpec, bool, error) {
	var root map[string]any
	if err := yaml.Unmarshal(body, &root); err != nil {
		return nil, false, nil
	}
	proxies, ok := root["proxies"].([]any)
	if !ok {
		return nil, false, nil
	}
	return nodesFromMaps(proxies, FormatClash, "name"), true, nil
}

func nodesFromMaps(entries []any, format Format, nameKey string) []NodeSpec {
	nodes := make([]NodeSpec, 0, len(entries))
	for _, entry := range entries {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		typeName, _ := item["type"].(string)
		typeName = strings.ToLower(strings.TrimSpace(typeName))
		if typeName == "" || ignoredOutboundTypes[typeName] {
			continue
		}
		tag, _ := item[nameKey].(string)
		raw, err := json.Marshal(item)
		if err != nil {
			continue
		}
		nodes = append(nodes, NodeSpec{
			ID:      stableMapID(item),
			Tag:     tag,
			Type:    typeName,
			Format:  format,
			Options: raw,
		})
	}
	return nodes
}

func stableMapID(input map[string]any) string {
	canonical := cloneMap(input)
	delete(canonical, "tag")
	delete(canonical, "name")
	if port, ok := canonical["port"]; ok {
		canonical["server_port"] = port
		delete(canonical, "port")
	}
	raw, _ := json.Marshal(canonical)
	return digest(raw)
}

func cloneMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		output[key] = input[key]
	}
	return output
}

func decodeBase64(text string) (string, bool) {
	compact := strings.Join(strings.Fields(text), "")
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		decoded, err := encoding.DecodeString(compact)
		if err == nil && strings.Contains(string(decoded), "://") {
			return string(decoded), true
		}
	}
	return text, false
}

func parseURIs(text string) ([]NodeSpec, error) {
	var nodes []NodeSpec
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parsed, err := url.Parse(line)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			continue
		}
		tag := parsed.Fragment
		parsed.Fragment = ""
		canonical := parsed.String()
		raw, _ := json.Marshal(canonical)
		nodes = append(nodes, NodeSpec{
			ID:      digest([]byte(canonical)),
			Tag:     tag,
			Type:    strings.ToLower(parsed.Scheme),
			Format:  FormatURI,
			Options: raw,
		})
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("subscription format is unsupported or contains no proxy nodes")
	}
	return nodes, nil
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:12])
}
