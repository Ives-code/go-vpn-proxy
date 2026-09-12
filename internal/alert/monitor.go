package alert

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"dual-egress-gateway/internal/pool"
)

type Sender interface {
	Send(context.Context, string, string) error
}

type Monitor struct {
	mu              sync.Mutex
	sender          Sender
	threshold       int
	sourceLabels    map[string]string
	alertedSources  map[string]bool
	lowNodesAlerted bool
	expiryAlerted   map[string]time.Time
}

func NewMonitor(sender Sender, threshold int, sourceLabels map[string]string) *Monitor {
	if threshold < 1 {
		threshold = 30
	}
	labels := make(map[string]string, len(sourceLabels))
	for id, label := range sourceLabels {
		labels[id] = label
	}
	return &Monitor{
		sender:         sender,
		threshold:      threshold,
		sourceLabels:   labels,
		alertedSources: make(map[string]bool),
		expiryAlerted:  make(map[string]time.Time),
	}
}

func SourceLabels(subscriptionURLs []string) map[string]string {
	labels := make(map[string]string, len(subscriptionURLs))
	for index, rawURL := range subscriptionURLs {
		id := strconv.Itoa(index + 1)
		parsed, err := url.Parse(rawURL)
		if err == nil && parsed.Hostname() != "" {
			labels[id] = parsed.Hostname()
		} else {
			labels[id] = "未知域名"
		}
	}
	return labels
}

func (monitor *Monitor) Evaluate(ctx context.Context, sourceErrors map[string]string, stats pool.Stats) []error {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()

	for id := range monitor.alertedSources {
		if _, failed := sourceErrors[id]; !failed {
			delete(monitor.alertedSources, id)
		}
	}

	var failures []error
	failedIDs := make([]string, 0, len(sourceErrors))
	for id := range sourceErrors {
		failedIDs = append(failedIDs, id)
	}
	sort.Strings(failedIDs)
	for _, id := range failedIDs {
		if monitor.alertedSources[id] {
			continue
		}
		label := monitor.sourceLabels[id]
		if label == "" {
			label = "未知域名"
		}
		body := fmt.Sprintf(
			"订阅 %s（%s）刷新失败，请检查订阅是否失效或网络是否异常。如有上次成功的节点缓存，网关会继续使用。当前可用节点 %d/%d。",
			id, label, stats.Healthy, stats.Total,
		)
		if monitor.sender == nil || monitor.sender.Send(ctx, "代理订阅异常", body) != nil {
			failures = append(failures, fmt.Errorf("subscription alert delivery failed for source %s", id))
			continue
		}
		monitor.alertedSources[id] = true
	}

	if stats.Healthy >= monitor.threshold {
		monitor.lowNodesAlerted = false
	} else if !monitor.lowNodesAlerted {
		body := fmt.Sprintf(
			"可用代理节点仅 %d 个，已低于告警阈值 %d 个。总节点 %d 个，不可用 %d 个，请检查订阅和节点状态。",
			stats.Healthy, monitor.threshold, stats.Total, stats.Unhealthy,
		)
		if monitor.sender == nil || monitor.sender.Send(ctx, "代理可用节点不足", body) != nil {
			failures = append(failures, errors.New("low-node alert delivery failed"))
		} else {
			monitor.lowNodesAlerted = true
		}
	}
	return failures
}

func (monitor *Monitor) EvaluateExpiry(ctx context.Context, expiries map[string]time.Time, now time.Time) []error {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	var failures []error
	for id, expiry := range expiries {
		if expiry.IsZero() || expiry.After(now.Add(7*24*time.Hour)) || monitor.expiryAlerted[id].Equal(expiry) {
			continue
		}
		title := "代理订阅续费提醒"
		state := "将在一周内到期，请及时续费"
		if !expiry.After(now) {
			state = "已到期，请尽快续费"
		}
		body := fmt.Sprintf("订阅 %s（%s）%s。到期时间：%s（北京时间）。", id, monitor.sourceLabels[id], state, expiry.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04"))
		if monitor.sender == nil || monitor.sender.Send(ctx, title, body) != nil {
			failures = append(failures, errors.New("expiry alert delivery failed"))
		} else {
			monitor.expiryAlerted[id] = expiry
		}
	}
	return failures
}
