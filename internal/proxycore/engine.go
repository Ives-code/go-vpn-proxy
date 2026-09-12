package proxycore

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"dual-egress-gateway/internal/pool"
	"dual-egress-gateway/internal/subscription"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	adaptercertificate "github.com/sagernet/sing-box/adapter/certificate"
	adapterendpoint "github.com/sagernet/sing-box/adapter/endpoint"
	adapterinbound "github.com/sagernet/sing-box/adapter/inbound"
	adapteroutbound "github.com/sagernet/sing-box/adapter/outbound"
	adapterservice "github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/dns/transport/local"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/direct"
	protocolhttp "github.com/sagernet/sing-box/protocol/http"
	"github.com/sagernet/sing-box/protocol/vless"
	singjson "github.com/sagernet/sing/common/json"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"
)

var _ pool.Factory = (*Engine)(nil)

var nonProxyTypes = map[string]bool{
	"block": true, "direct": true, "dns": true, "selector": true, "urltest": true,
}

type Engine struct {
	mu      sync.Mutex
	boxCtx  context.Context
	box     *box.Box
	logger  log.ContextLogger
	closed  bool
	builtBy map[string]string
}

func NewEngine(ctx context.Context) (*Engine, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	boxCtx := minimalBoxContext(ctx)
	instance, err := box.New(box.Options{
		Context: boxCtx,
		Options: option.Options{Log: &option.LogOptions{Disabled: true}},
	})
	if err != nil {
		return nil, fmt.Errorf("initialize sing-box engine: %w", err)
	}
	if err := instance.Start(); err != nil {
		_ = instance.Close()
		return nil, fmt.Errorf("start sing-box engine: %w", err)
	}
	factory := log.NewNOPFactory()
	return &Engine{
		boxCtx:  boxCtx,
		box:     instance,
		logger:  factory.NewLogger("dynamic-outbound"),
		builtBy: make(map[string]string),
	}, nil
}

func minimalBoxContext(ctx context.Context) context.Context {
	inboundRegistry := adapterinbound.NewRegistry()
	outboundRegistry := minimalOutboundRegistry()
	endpointRegistry := adapterendpoint.NewRegistry()
	dnsRegistry := dns.NewTransportRegistry()
	local.RegisterTransport(dnsRegistry)
	serviceRegistry := adapterservice.NewRegistry()
	certificateRegistry := adaptercertificate.NewRegistry()
	boxCtx := box.Context(
		ctx,
		inboundRegistry,
		outboundRegistry,
		endpointRegistry,
		dnsRegistry,
		serviceRegistry,
		certificateRegistry,
	)
	return service.ContextWith[adapter.PlatformInterface](boxCtx, (*platformStub)(nil))
}

func minimalOutboundRegistry() *adapteroutbound.Registry {
	registry := adapteroutbound.NewRegistry()
	direct.RegisterOutbound(registry)
	protocolhttp.RegisterOutbound(registry)
	vless.RegisterOutbound(registry)
	return registry
}

func (engine *Engine) Build(spec subscription.NodeSpec) (pool.Dialer, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.closed {
		return nil, errors.New("sing-box engine is closed")
	}
	typeName := strings.ToLower(strings.TrimSpace(spec.Type))
	if typeName == "" || nonProxyTypes[typeName] {
		return nil, fmt.Errorf("outbound type %q is not a proxy node", typeName)
	}
	if spec.Format != subscription.FormatSingBox {
		return nil, fmt.Errorf("outbound format %q is not yet normalized to sing-box", spec.Format)
	}

	var outboundOptions option.Outbound
	if err := singjson.UnmarshalContext(engine.boxCtx, spec.Options, &outboundOptions); err != nil {
		return nil, fmt.Errorf("invalid %s outbound options", typeName)
	}
	if outboundOptions.Type != typeName || nonProxyTypes[outboundOptions.Type] {
		return nil, fmt.Errorf("outbound type %q is not a proxy node", outboundOptions.Type)
	}

	tag := "node-" + spec.ID
	if previousID, exists := engine.builtBy[tag]; exists {
		return nil, fmt.Errorf("outbound %s already exists", previousID)
	}
	if err := engine.box.Outbound().Create(
		engine.boxCtx,
		engine.box.Router(),
		engine.logger,
		tag,
		outboundOptions.Type,
		outboundOptions.Options,
	); err != nil {
		return nil, fmt.Errorf("create %s outbound", typeName)
	}
	outbound, ok := engine.box.Outbound().Outbound(tag)
	if !ok {
		return nil, errors.New("created outbound is unavailable")
	}
	engine.builtBy[tag] = spec.ID
	return &nodeDialer{engine: engine, tag: tag, outbound: outbound}, nil
}

func (engine *Engine) Close() error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.closed {
		return nil
	}
	engine.closed = true
	engine.builtBy = nil
	return engine.box.Close()
}

type nodeDialer struct {
	engine   *Engine
	tag      string
	outbound adapter.Outbound
	once     sync.Once
	err      error
}

func (dialer *nodeDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	destination := M.ParseSocksaddr(address)
	if !destination.IsValid() {
		return nil, errors.New("invalid destination address")
	}
	return dialer.outbound.DialContext(ctx, network, destination)
}

func (dialer *nodeDialer) Close() error {
	dialer.once.Do(func() {
		dialer.engine.mu.Lock()
		defer dialer.engine.mu.Unlock()
		if dialer.engine.closed {
			return
		}
		dialer.err = dialer.engine.box.Outbound().Remove(dialer.tag)
		delete(dialer.engine.builtBy, dialer.tag)
	})
	return dialer.err
}
