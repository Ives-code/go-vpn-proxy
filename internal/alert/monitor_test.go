package alert

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dual-egress-gateway/internal/pool"
)

func TestExpiryReminderBoundaryRenewalAndRetry(t *testing.T) {
	s := &recordingSender{}
	m := NewMonitor(s, 30, map[string]string{"1": "example.com"})
	now := time.Unix(1800000000, 0)
	expiry := now.Add(7 * 24 * time.Hour)
	m.EvaluateExpiry(context.Background(), map[string]time.Time{"1": expiry.Add(time.Second)}, now)
	if len(s.messages) != 0 {
		t.Fatal("too early")
	}
	s.failures = 1
	m.EvaluateExpiry(context.Background(), map[string]time.Time{"1": expiry}, now)
	m.EvaluateExpiry(context.Background(), map[string]time.Time{"1": expiry}, now)
	m.EvaluateExpiry(context.Background(), map[string]time.Time{"1": expiry}, now)
	if len(s.messages) != 2 || !strings.Contains(s.messages[1].body, "续费") {
		t.Fatal("retry or deduplication failed")
	}
	m.EvaluateExpiry(context.Background(), map[string]time.Time{"1": expiry.Add(24 * time.Hour)}, now.Add(24*time.Hour))
	if len(s.messages) != 3 {
		t.Fatal("renewed expiry did not rearm")
	}
}

type sentMessage struct {
	title string
	body  string
}

type recordingSender struct {
	messages []sentMessage
	failures int
}

func (sender *recordingSender) Send(_ context.Context, title, body string) error {
	sender.messages = append(sender.messages, sentMessage{title: title, body: body})
	if sender.failures > 0 {
		sender.failures--
		return errors.New("delivery failed")
	}
	return nil
}

func TestSubscriptionFailureAlertsOnceAndRearmsAfterRecovery(t *testing.T) {
	sender := &recordingSender{}
	labels := SourceLabels([]string{"https://subscription.example/private/path?token=never-print"})
	monitor := NewMonitor(sender, 30, labels)
	stats := pool.Stats{Total: 50, Healthy: 40, Unhealthy: 10}

	monitor.Evaluate(context.Background(), map[string]string{"1": "secret failure detail"}, stats)
	monitor.Evaluate(context.Background(), map[string]string{"1": "secret failure detail"}, stats)
	if len(sender.messages) != 1 {
		t.Fatalf("messages after repeated failure = %d", len(sender.messages))
	}
	message := sender.messages[0]
	if !strings.Contains(message.title, "订阅") || !strings.Contains(message.body, "订阅 1") || !strings.Contains(message.body, "subscription.example") {
		t.Fatalf("message does not identify safe source: %#v", message)
	}
	if strings.Contains(message.body, "private") || strings.Contains(message.body, "token") || strings.Contains(message.body, "secret failure") {
		t.Fatalf("message leaked source details: %#v", message)
	}

	monitor.Evaluate(context.Background(), nil, stats)
	monitor.Evaluate(context.Background(), map[string]string{"1": "failed again"}, stats)
	if len(sender.messages) != 2 {
		t.Fatalf("messages after recovery and failure = %d", len(sender.messages))
	}
}

func TestLowHealthyNodeAlertUsesStrictThresholdAndRearms(t *testing.T) {
	sender := &recordingSender{}
	monitor := NewMonitor(sender, 30, nil)

	monitor.Evaluate(context.Background(), nil, pool.Stats{Total: 40, Healthy: 30, Unhealthy: 10})
	monitor.Evaluate(context.Background(), nil, pool.Stats{Total: 40, Healthy: 29, Unhealthy: 11})
	monitor.Evaluate(context.Background(), nil, pool.Stats{Total: 40, Healthy: 20, Unhealthy: 20})
	if len(sender.messages) != 1 {
		t.Fatalf("messages during one low period = %d", len(sender.messages))
	}
	if !strings.Contains(sender.messages[0].body, "29") || !strings.Contains(sender.messages[0].body, "30") {
		t.Fatalf("low-node message lacks count and threshold: %#v", sender.messages[0])
	}

	monitor.Evaluate(context.Background(), nil, pool.Stats{Total: 40, Healthy: 30, Unhealthy: 10})
	monitor.Evaluate(context.Background(), nil, pool.Stats{Total: 40, Healthy: 29, Unhealthy: 11})
	if len(sender.messages) != 2 {
		t.Fatalf("messages after low-state recovery = %d", len(sender.messages))
	}
}

func TestFailedDeliveryIsRetriedOnNextEvaluation(t *testing.T) {
	sender := &recordingSender{failures: 1}
	monitor := NewMonitor(sender, 30, map[string]string{"2": "safe.example"})
	stats := pool.Stats{Total: 50, Healthy: 40, Unhealthy: 10}

	first := monitor.Evaluate(context.Background(), map[string]string{"2": "failed"}, stats)
	second := monitor.Evaluate(context.Background(), map[string]string{"2": "failed"}, stats)
	if len(first) != 1 || len(second) != 0 || len(sender.messages) != 2 {
		t.Fatalf("failures=(%d,%d) attempts=%d", len(first), len(second), len(sender.messages))
	}
}
