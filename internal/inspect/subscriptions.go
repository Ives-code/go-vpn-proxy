package inspect

import (
	"context"
	"dual-egress-gateway/internal/admin"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"text/tabwriter"
	"time"
)

// Subscriptions only reads the loopback status endpoint; it never refreshes state.
func Subscriptions(ctx context.Context, endpoint string, out io.Writer) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("管理地址必须为本机 HTTP 地址，例如 http://127.0.0.1:19090")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("管理地址必须为本机回环地址")
	}
	u.Path = "/status"
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return errors.New("管理地址无效")
	}
	response, err := client.Do(req)
	if err != nil {
		return errors.New("无法连接管理接口，请确认网关运行状态和管理端口")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("管理接口返回 HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(body) > 2<<20 {
		return errors.New("读取状态失败或响应过大")
	}
	var status admin.Status
	if json.Unmarshal(body, &status) != nil {
		return errors.New("管理接口返回了无效状态")
	}
	if status.Subscriptions == nil {
		return errors.New("运行中的网关尚不支持逐订阅查询；请在允许维护时更新并重启网关。本命令未修改运行状态")
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "编号\t订阅地址（脱敏）\t刷新状态\t生效状态\t到期时间（北京时间）\t解析节点\t加载节点\t可用\t不可用\t待检测\n")
	now := time.Now()
	for _, s := range status.Subscriptions {
		refresh := "待刷新"
		switch s.RefreshState {
		case "ok":
			refresh = "成功"
		case "failed":
			refresh = "失败"
			if s.Nodes.Total > 0 {
				refresh = "失败（使用缓存）"
			}
		}
		effective := "未加载"
		if s.Nodes.Total > 0 {
			effective = "已加载，无可用节点"
			if s.Nodes.Healthy > 0 {
				effective = "生效"
			}
		}
		expiry := "未知"
		if !s.ExpiresAt.IsZero() {
			expiry = s.ExpiresAt.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04")
			if !s.ExpiresAt.After(now) {
				expiry += "（已过期）"
			} else if !s.ExpiresAt.After(now.Add(7 * 24 * time.Hour)) {
				expiry += "（7天内到期）"
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\n", clean(s.ID), clean(s.Address), refresh, effective, expiry, s.ParsedNodes, s.Nodes.Total, s.Nodes.Healthy, s.Nodes.Unhealthy, s.Nodes.Quarantined)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "\n去重后的总池：%d 个节点，可用 %d 个。共享节点会计入所属的每条订阅，分项数量不能直接相加。\n", status.Nodes.Total, status.Nodes.Healthy)
	return err
}

func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, s)
}
