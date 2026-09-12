package redact

import (
	"regexp"
	"sort"
	"strings"
)

var (
	urlPattern      = regexp.MustCompile(`(?i)https?://[^\s]+`)
	ipv4PortPattern = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?\b`)
	hostPortPattern = regexp.MustCompile(`\b(?:[a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}(?::\d{1,5})\b`)
)

func Text(input string, secrets ...string) string {
	output := urlPattern.ReplaceAllString(input, "[REDACTED_URL]")
	output = ipv4PortPattern.ReplaceAllString(output, "[REDACTED_ENDPOINT]")
	output = hostPortPattern.ReplaceAllString(output, "[REDACTED_ENDPOINT]")

	filtered := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if secret != "" {
			filtered = append(filtered, secret)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return len(filtered[i]) > len(filtered[j]) })
	for _, secret := range filtered {
		output = strings.ReplaceAll(output, secret, "[REDACTED]")
	}
	return output
}
