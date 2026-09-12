package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const redacted = "[REDACTED]"

type Secret string

func (Secret) String() string   { return redacted }
func (Secret) GoString() string { return redacted }

func (secret Secret) Reveal() string { return string(secret) }

type Config struct {
	HTTPListen          string
	WSListen            string
	AdminListen         string
	Username            Secret
	Password            Secret
	AdminToken          Secret
	PushBaseURL         Secret
	SubscriptionURLs    []string
	LowNodeThreshold    int
	RefreshInterval     time.Duration
	ProbeInterval       time.Duration
	DialTimeout         time.Duration
	ShutdownDrain       time.Duration
	MaxSubscriptionSize int64
	ProbeURL            string
}

type fileConfig struct {
	HTTPListen          string `yaml:"http_listen"`
	WSListen            string `yaml:"ws_listen"`
	AdminListen         string `yaml:"admin_listen"`
	RefreshInterval     string `yaml:"refresh_interval"`
	ProbeInterval       string `yaml:"probe_interval"`
	DialTimeout         string `yaml:"dial_timeout"`
	ShutdownDrain       string `yaml:"shutdown_drain"`
	MaxSubscriptionSize string `yaml:"max_subscription_size"`
	ProbeURL            string `yaml:"probe_url"`
	LowNodeThreshold    int    `yaml:"low_node_threshold"`
}

func Load(path string, lookupEnv func(string) (string, bool)) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var file fileConfig
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	cfg := Config{
		HTTPListen:          defaultString(file.HTTPListen, "127.0.0.1:18080"),
		WSListen:            defaultString(file.WSListen, "127.0.0.1:18081"),
		AdminListen:         defaultString(file.AdminListen, "127.0.0.1:19090"),
		RefreshInterval:     30 * time.Minute,
		ProbeInterval:       30 * time.Second,
		DialTimeout:         10 * time.Second,
		ShutdownDrain:       30 * time.Second,
		MaxSubscriptionSize: 4 << 20,
		ProbeURL:            defaultString(file.ProbeURL, "https://cp.cloudflare.com/generate_204"),
		LowNodeThreshold:    30,
	}
	if file.LowNodeThreshold != 0 {
		if file.LowNodeThreshold < 1 {
			return Config{}, errors.New("low_node_threshold must be a positive integer")
		}
		cfg.LowNodeThreshold = file.LowNodeThreshold
	}

	for name, target := range map[string]*time.Duration{
		"refresh_interval": &cfg.RefreshInterval,
		"probe_interval":   &cfg.ProbeInterval,
		"dial_timeout":     &cfg.DialTimeout,
		"shutdown_drain":   &cfg.ShutdownDrain,
	} {
		value := map[string]string{
			"refresh_interval": file.RefreshInterval,
			"probe_interval":   file.ProbeInterval,
			"dial_timeout":     file.DialTimeout,
			"shutdown_drain":   file.ShutdownDrain,
		}[name]
		if value == "" {
			continue
		}
		parsed, parseErr := time.ParseDuration(value)
		if parseErr != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("%s must be a positive duration", name)
		}
		*target = parsed
	}

	if file.MaxSubscriptionSize != "" {
		parsed, parseErr := strconv.ParseInt(file.MaxSubscriptionSize, 10, 64)
		if parseErr != nil || parsed <= 0 {
			return Config{}, errors.New("max_subscription_size must be a positive byte count")
		}
		cfg.MaxSubscriptionSize = parsed
	}

	if cfg.HTTPListen == cfg.WSListen {
		return Config{}, errors.New("http_listen and ws_listen must differ")
	}
	if err := validateLoopback(cfg.AdminListen); err != nil {
		return Config{}, fmt.Errorf("admin_listen must use a loopback address: %w", err)
	}
	if err := validateProbeURL(cfg.ProbeURL); err != nil {
		return Config{}, err
	}

	var ok bool
	if cfg.Username, ok = requiredSecret("PROXY_USERNAME", lookupEnv); !ok {
		return Config{}, errors.New("PROXY_USERNAME is required")
	}
	if cfg.Password, ok = requiredSecret("PROXY_PASSWORD", lookupEnv); !ok {
		return Config{}, errors.New("PROXY_PASSWORD is required")
	}
	if cfg.AdminToken, ok = requiredSecret("ADMIN_TOKEN", lookupEnv); !ok {
		return Config{}, errors.New("ADMIN_TOKEN is required")
	}
	if value, exists := lookupEnv("PUSH_BASE_URL"); exists && strings.TrimSpace(value) != "" {
		value = strings.TrimSpace(value)
		if err := validatePushBaseURL(value); err != nil {
			return Config{}, err
		}
		cfg.PushBaseURL = Secret(value)
	}

	urlsValue, ok := lookupEnv("SUBSCRIPTION_URLS")
	if !ok || strings.TrimSpace(urlsValue) == "" {
		return Config{}, errors.New("SUBSCRIPTION_URLS is required")
	}
	for _, value := range strings.FieldsFunc(urlsValue, func(r rune) bool { return r == '\n' || r == '\r' }) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		parsed, parseErr := url.Parse(trimmed)
		if parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return Config{}, errors.New("every SUBSCRIPTION_URLS entry must be an absolute HTTPS URL")
		}
		cfg.SubscriptionURLs = append(cfg.SubscriptionURLs, trimmed)
	}
	if len(cfg.SubscriptionURLs) == 0 {
		return Config{}, errors.New("SUBSCRIPTION_URLS is required")
	}

	return cfg, nil
}

func requiredSecret(name string, lookupEnv func(string) (string, bool)) (Secret, bool) {
	value, ok := lookupEnv(name)
	value = strings.TrimSpace(value)
	return Secret(value), ok && value != ""
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func validateLoopback(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("host is not loopback")
	}
	return nil
}

func validateProbeURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("probe_url must be an absolute HTTPS URL")
	}
	return nil
}

func validatePushBaseURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("PUSH_BASE_URL must be an absolute HTTP or HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("PUSH_BASE_URL must not contain user information, a query, or a fragment")
	}
	return nil
}
