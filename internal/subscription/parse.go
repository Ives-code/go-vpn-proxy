package subscription

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var ignoredOutboundTypes = map[string]bool{
	"block": true, "direct": true, "dns": true, "selector": true, "urltest": true,
}

var supportedRuntimeTypes = map[string]bool{
	"anytls": true, "http": true, "hysteria2": true, "trojan": true, "vless": true, "shadowsocks": true,
}

var supportedNormalizedTypes = map[string]bool{"http": true, "vless": true, "anytls": true}

const (
	MaxNodesPerSource  = 512
	MaxStructureDepth  = 32
	maxStructureValues = 100000
)

func Parse(body []byte, _ string) ([]NodeSpec, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, errors.New("subscription is empty")
	}

	if nodes, recognized, err := parseJSON(trimmed); recognized {
		return enforceNodeLimit(nodes, err)
	}
	if nodes, recognized, err := parseClash(trimmed); recognized {
		return enforceNodeLimit(nodes, err)
	}

	text := string(trimmed)
	if nodes, recognized, err := parseWebshare(text); recognized {
		return enforceNodeLimit(nodes, err)
	}
	if decoded, ok := decodeBase64(text); ok {
		text = decoded
	}
	return enforceNodeLimit(parseURIs(text))
}

func parseWebshare(text string) ([]NodeSpec, bool, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	first := ""
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			first = strings.TrimSpace(line)
			break
		}
	}
	fields := strings.Split(first, ":")
	if len(fields) != 4 || net.ParseIP(fields[0]).To4() == nil {
		return nil, false, nil
	}
	nodes := make([]NodeSpec, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) != 4 || net.ParseIP(parts[0]).To4() == nil || parts[2] == "" || parts[3] == "" || strings.ContainsAny(parts[2]+parts[3], " \t\r\n") {
			return nil, true, errors.New("invalid Webshare proxy record")
		}
		port, err := strconv.Atoi(parts[1])
		if err != nil || port < 1 || port > 65535 {
			return nil, true, errors.New("invalid Webshare proxy port")
		}
		outbound := map[string]any{"type": "http", "server": parts[0], "server_port": port, "username": parts[2], "password": parts[3]}
		raw, _ := json.Marshal(outbound)
		nodes = append(nodes, NodeSpec{ID: stableMapID(outbound), Type: "http", Format: FormatSingBox, Options: raw})
	}
	return nodes, true, nil
}

func enforceNodeLimit(nodes []NodeSpec, err error) ([]NodeSpec, error) {
	if err != nil {
		return nil, err
	}
	if len(nodes) > MaxNodesPerSource {
		return nil, fmt.Errorf("subscription exceeds node limit of %d", MaxNodesPerSource)
	}
	return nodes, nil
}

func parseJSON(body []byte) ([]NodeSpec, bool, error) {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, false, nil
	}
	if err := validateStructure(root); err != nil {
		return nil, true, err
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
	if unsupportedType(entries, supportedRuntimeTypes) != "" {
		return nil, true, errors.New("subscription contains an unsupported proxy protocol")
	}

	return nodesFromMaps(entries, FormatSingBox, "tag"), true, nil
}

func parseClash(body []byte) ([]NodeSpec, bool, error) {
	var root map[string]any
	if err := yaml.Unmarshal(body, &root); err != nil {
		return nil, false, nil
	}
	if err := validateStructure(root); err != nil {
		return nil, true, err
	}
	proxies, ok := root["proxies"].([]any)
	if !ok {
		return nil, false, nil
	}
	if unsupportedType(proxies, supportedNormalizedTypes) != "" {
		return nil, true, errors.New("subscription contains an unsupported proxy protocol")
	}
	for _, entry := range proxies {
		if item, ok := entry.(map[string]any); ok {
			if err := validateClashNode(item); err != nil {
				return nil, true, err
			}
		}
	}
	return nodesFromClash(proxies), true, nil
}

func validateClashNode(input map[string]any) error {
	if strings.ToLower(stringValue(input, "type")) == "anytls" {
		if stringValue(input, "password") == "" {
			return errors.New("AnyTLS password is required")
		}
		for _, key := range []string{"idle-session-check-interval", "idle-session-timeout", "min-idle-session"} {
			if raw, exists := input[key]; exists {
				if n, ok := intValue(raw); !ok || n < 0 {
					return errors.New("invalid AnyTLS session option")
				}
			}
		}
		return nil
	}
	if strings.ToLower(stringValue(input, "type")) != "vless" {
		return nil
	}
	security := strings.ToLower(stringValue(input, "security"))
	if !oneOf(security, "", "none", "tls", "reality") {
		return errors.New("subscription contains unsupported VLESS security")
	}
	network := strings.ToLower(stringValue(input, "network"))
	if !oneOf(network, "", "tcp", "ws", "websocket", "grpc", "http", "h2") {
		return errors.New("subscription contains unsupported VLESS transport")
	}
	realityValue, hasReality := input["reality-opts"]
	reality, realityIsMap := realityValue.(map[string]any)
	if hasReality && !realityIsMap {
		return errors.New("subscription contains unsupported malformed VLESS Reality options")
	}
	if security == "reality" && (!realityIsMap || stringValue(reality, "public-key") == "") {
		return errors.New("subscription contains unsupported incomplete VLESS Reality options")
	}
	if realityIsMap && stringValue(reality, "public-key") == "" {
		return errors.New("subscription contains unsupported incomplete VLESS Reality options")
	}
	return nil
}

func validateStructure(root any) error {
	count := 0
	var walk func(any, int) error
	walk = func(value any, depth int) error {
		if depth > MaxStructureDepth {
			return errors.New("subscription structure is too complex")
		}
		count++
		if count > maxStructureValues {
			return errors.New("subscription structure is too complex")
		}
		switch typed := value.(type) {
		case map[string]any:
			for _, child := range typed {
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range typed {
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(root, 0)
}

func unsupportedType(entries []any, allowed map[string]bool) string {
	for _, entry := range entries {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		typeName := strings.ToLower(stringValue(item, "type"))
		if typeName != "" && !ignoredOutboundTypes[typeName] && !allowed[typeName] {
			return typeName
		}
	}
	return ""
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
		if typeName == "" || ignoredOutboundTypes[typeName] || !supportedRuntimeTypes[typeName] {
			continue
		}
		if format == FormatSingBox && typeName == "vless" {
			item = normalizeSingBoxVLESS(item)
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

func normalizeSingBoxVLESS(input map[string]any) map[string]any {
	output := cloneMap(input)
	tlsOptions, ok := input["tls"].(map[string]any)
	if !ok {
		return output
	}
	reality, ok := tlsOptions["reality"].(map[string]any)
	if !ok || !boolValue(reality["enabled"]) {
		return output
	}
	if _, hasUTLS := tlsOptions["utls"]; hasUTLS {
		return output
	}
	tlsCopy := cloneMap(tlsOptions)
	tlsCopy["utls"] = map[string]any{"enabled": true, "fingerprint": "chrome"}
	output["tls"] = tlsCopy
	return output
}

func nodesFromClash(entries []any) []NodeSpec {
	nodes := make([]NodeSpec, 0, len(entries))
	for _, entry := range entries {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		normalized, ok := normalizeClashNode(item)
		if !ok {
			continue
		}
		tag, _ := normalized["tag"].(string)
		typeName, _ := normalized["type"].(string)
		raw, _ := json.Marshal(normalized)
		nodes = append(nodes, NodeSpec{ID: stableMapID(normalized), Tag: tag, Type: typeName, Format: FormatSingBox, Options: raw})
	}
	return nodes
}

func normalizeClashNode(input map[string]any) (map[string]any, bool) {
	typeName := strings.ToLower(stringValue(input, "type"))
	if !supportedNormalizedTypes[typeName] {
		return nil, false
	}
	server := stringValue(input, "server")
	port, ok := intValue(input["port"])
	if server == "" || !ok || port < 1 || port > 65535 {
		return nil, false
	}
	output := map[string]any{"type": typeName, "tag": stringValue(input, "name"), "server": server, "server_port": port}
	if typeName == "anytls" {
		copyString(output, input, "password")
		output["tls"] = clashTLS(input)
		for _, key := range []string{"idle-session-check-interval", "idle-session-timeout", "min-idle-session"} {
			if raw, exists := input[key]; exists {
				n, _ := intValue(raw)
				target := strings.ReplaceAll(key, "-", "_")
				if key == "min-idle-session" {
					output[target] = n
				} else {
					output[target] = fmt.Sprintf("%ds", n)
				}
			}
		}
		return output, true
	}
	if typeName == "http" {
		copyString(output, input, "username")
		copyString(output, input, "password")
		if boolValue(input["tls"]) {
			output["tls"] = clashTLS(input)
		}
		return output, true
	}
	uuid := stringValue(input, "uuid")
	if uuid == "" {
		return nil, false
	}
	output["uuid"] = uuid
	copyString(output, input, "flow")
	_, hasReality := input["reality-opts"].(map[string]any)
	if boolValue(input["tls"]) || oneOf(strings.ToLower(stringValue(input, "security")), "tls", "reality") || hasReality {
		output["tls"] = clashTLS(input)
	}
	if transport := clashTransport(input); transport != nil {
		output["transport"] = transport
	}
	return output, true
}

func clashTLS(input map[string]any) map[string]any {
	tlsOptions := map[string]any{"enabled": true}
	serverName := firstNonEmpty(stringValue(input, "servername"), stringValue(input, "sni"))
	if serverName != "" {
		tlsOptions["server_name"] = serverName
	}
	if boolValue(input["skip-cert-verify"]) {
		tlsOptions["insecure"] = true
	}
	if fingerprint := stringValue(input, "client-fingerprint"); fingerprint != "" {
		tlsOptions["utls"] = map[string]any{"enabled": true, "fingerprint": fingerprint}
	}
	if reality, ok := input["reality-opts"].(map[string]any); ok {
		realityOptions := map[string]any{"enabled": true}
		if key := stringValue(reality, "public-key"); key != "" {
			realityOptions["public_key"] = key
		}
		if shortID := stringValue(reality, "short-id"); shortID != "" {
			realityOptions["short_id"] = shortID
		}
		tlsOptions["reality"] = realityOptions
	}
	return tlsOptions
}

func clashTransport(input map[string]any) map[string]any {
	switch strings.ToLower(stringValue(input, "network")) {
	case "ws", "websocket":
		transport := map[string]any{"type": "ws"}
		if options, ok := input["ws-opts"].(map[string]any); ok {
			copyString(transport, options, "path")
			if headers, ok := options["headers"].(map[string]any); ok {
				transport["headers"] = headers
			}
		}
		return transport
	case "grpc":
		transport := map[string]any{"type": "grpc"}
		if options, ok := input["grpc-opts"].(map[string]any); ok {
			if value := firstNonEmpty(stringValue(options, "grpc-service-name"), stringValue(options, "service-name")); value != "" {
				transport["service_name"] = value
			}
		}
		return transport
	case "http", "h2":
		return map[string]any{"type": "http"}
	default:
		return nil
	}
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
	unsupported := false
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parsed, err := url.Parse(line)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			continue
		}
		if strings.EqualFold(parsed.Scheme, "vless") {
			if err := validateVLESSURI(parsed); err != nil {
				return nil, err
			}
		}
		normalized, ok := normalizeURINode(parsed)
		if !ok {
			if scheme := strings.ToLower(parsed.Scheme); scheme != "vless" && scheme != "http" && scheme != "https" {
				unsupported = true
			}
			continue
		}
		raw, _ := json.Marshal(normalized)
		nodes = append(nodes, NodeSpec{
			ID:      stableMapID(normalized),
			Tag:     parsed.Fragment,
			Type:    strings.ToLower(stringValue(normalized, "type")),
			Format:  FormatSingBox,
			Options: raw,
		})
	}
	if unsupported {
		return nil, fmt.Errorf("subscription contains an unsupported proxy protocol")
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("subscription contains no supported proxy nodes")
	}
	return nodes, nil
}

func validateVLESSURI(parsed *url.URL) error {
	query := parsed.Query()
	security := strings.ToLower(query.Get("security"))
	if !oneOf(security, "", "none", "tls", "reality") {
		return errors.New("subscription contains unsupported VLESS security")
	}
	transport := strings.ToLower(query.Get("type"))
	if !oneOf(transport, "", "tcp", "ws", "websocket", "grpc", "http", "h2") {
		return errors.New("subscription contains unsupported VLESS transport")
	}
	if security == "reality" && query.Get("pbk") == "" {
		return errors.New("subscription contains unsupported incomplete VLESS Reality options")
	}
	return nil
}

func normalizeURINode(parsed *url.URL) (map[string]any, bool) {
	scheme := strings.ToLower(parsed.Scheme)
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 || parsed.Hostname() == "" {
		return nil, false
	}
	if scheme == "http" || scheme == "https" {
		output := map[string]any{"type": "http", "tag": parsed.Fragment, "server": parsed.Hostname(), "server_port": port}
		if parsed.User != nil {
			output["username"] = parsed.User.Username()
			if password, ok := parsed.User.Password(); ok {
				output["password"] = password
			}
		}
		if scheme == "https" {
			output["tls"] = map[string]any{"enabled": true, "server_name": parsed.Hostname()}
		}
		return output, true
	}
	if scheme != "vless" || parsed.User == nil || parsed.User.Username() == "" {
		return nil, false
	}
	query := parsed.Query()
	output := map[string]any{
		"type": "vless", "tag": parsed.Fragment, "server": parsed.Hostname(), "server_port": port, "uuid": parsed.User.Username(),
	}
	if flow := query.Get("flow"); flow != "" {
		output["flow"] = flow
	}
	security := strings.ToLower(query.Get("security"))
	if security == "tls" || security == "reality" {
		tlsOptions := map[string]any{"enabled": true}
		if serverName := firstNonEmpty(query.Get("sni"), query.Get("servername"), query.Get("peer")); serverName != "" {
			tlsOptions["server_name"] = serverName
		}
		if queryTruthy(query, "allowInsecure") || queryTruthy(query, "insecure") {
			tlsOptions["insecure"] = true
		}
		if fingerprint := firstNonEmpty(query.Get("fp"), query.Get("fingerprint")); fingerprint != "" {
			tlsOptions["utls"] = map[string]any{"enabled": true, "fingerprint": fingerprint}
		}
		if security == "reality" {
			tlsOptions["reality"] = map[string]any{"enabled": true, "public_key": query.Get("pbk"), "short_id": query.Get("sid")}
		}
		output["tls"] = tlsOptions
	}
	if transport := uriTransport(query); transport != nil {
		output["transport"] = transport
	}
	return output, true
}

func uriTransport(query url.Values) map[string]any {
	switch strings.ToLower(query.Get("type")) {
	case "ws", "websocket":
		transport := map[string]any{"type": "ws"}
		if path := query.Get("path"); path != "" {
			transport["path"] = path
		}
		if host := query.Get("host"); host != "" {
			transport["headers"] = map[string]any{"Host": host}
		}
		return transport
	case "grpc":
		transport := map[string]any{"type": "grpc"}
		if serviceName := firstNonEmpty(query.Get("serviceName"), query.Get("service_name")); serviceName != "" {
			transport["service_name"] = serviceName
		}
		return transport
	case "http", "h2":
		return map[string]any{"type": "http"}
	default:
		return nil
	}
}

func stringValue(input map[string]any, key string) string {
	value, _ := input[key].(string)
	return strings.TrimSpace(value)
}

func intValue(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case int64:
		return int(number), true
	case float64:
		return int(number), number == float64(int(number))
	case string:
		parsed, err := strconv.Atoi(number)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(typed, "true") || typed == "1"
	default:
		return false
	}
}

func copyString(destination, source map[string]any, key string) {
	if value := stringValue(source, key); value != "" {
		destination[key] = value
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func queryTruthy(query url.Values, key string) bool {
	value := query.Get(key)
	return value == "1" || strings.EqualFold(value, "true")
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:12])
}
