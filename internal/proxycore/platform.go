package proxycore

import (
	"context"
	"net/netip"
	"os"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	tun "github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/control"
	"github.com/sagernet/sing/common/logger"
	"github.com/sagernet/sing/common/x/list"
)

// platformStub deliberately disables sing-box's asynchronous default-interface
// monitor. This daemon does not use TUN or interface-aware routing; normal
// outbound sockets follow the host routing table. The no-op monitor also avoids
// the upstream NetworkManager.started race present in sing-box v1.14.0.
type platformStub struct{}

func (*platformStub) Initialize(adapter.NetworkManager) error     { return nil }
func (*platformStub) UsePlatformAutoDetectInterfaceControl() bool { return false }
func (*platformStub) AutoDetectInterfaceControl(int) error        { return nil }
func (*platformStub) UsePlatformInterface() bool                  { return false }
func (*platformStub) OpenInterface(*tun.Options, option.TunPlatformOptions) (tun.Tun, error) {
	return nil, os.ErrInvalid
}
func (*platformStub) ProcessPlatformOptions(option.TunPlatformOptions) error { return nil }
func (*platformStub) UsePlatformDefaultInterfaceMonitor() bool               { return true }
func (*platformStub) CreateDefaultInterfaceMonitor(logger.Logger) tun.DefaultInterfaceMonitor {
	return (*interfaceMonitorStub)(nil)
}
func (*platformStub) UsePlatformNetworkInterfaces() bool { return false }
func (*platformStub) NetworkInterfaces() ([]adapter.NetworkInterface, error) {
	return nil, os.ErrInvalid
}
func (*platformStub) UnderNetworkExtension() bool                     { return false }
func (*platformStub) NetworkExtensionIncludeAllNetworks() bool        { return false }
func (*platformStub) ClearDNSCache()                                  {}
func (*platformStub) RequestPermissionForWIFIState() error            { return nil }
func (*platformStub) ReadWIFIState(context.Context) adapter.WIFIState { return adapter.WIFIState{} }
func (*platformStub) UsePlatformConnectionOwnerFinder() bool          { return false }
func (*platformStub) FindConnectionOwner(*adapter.FindConnectionOwnerRequest) (*adapter.ConnectionOwner, error) {
	return nil, os.ErrInvalid
}
func (*platformStub) UsePlatformWIFIMonitor() bool                              { return false }
func (*platformStub) UsePlatformNotification() bool                             { return false }
func (*platformStub) SendNotification(*adapter.Notification) error              { return nil }
func (*platformStub) CancelNotification(string, int32) error                    { return nil }
func (*platformStub) MyInterfaceAddress() []netip.Addr                          { return nil }
func (*platformStub) UsePlatformNeighborResolver() bool                         { return false }
func (*platformStub) StartNeighborMonitor(adapter.NeighborUpdateListener) error { return os.ErrInvalid }
func (*platformStub) CloseNeighborMonitor(adapter.NeighborUpdateListener) error { return nil }
func (*platformStub) UsePlatformShell() bool                                    { return false }
func (*platformStub) CheckPlatformShell() error                                 { return nil }
func (*platformStub) OpenShellSession(*adapter.PlatformUser, string, []string, string, int32, int32) (adapter.ShellSession, error) {
	return nil, os.ErrInvalid
}
func (*platformStub) LookupUser(string) (*adapter.PlatformUser, error) { return nil, os.ErrInvalid }
func (*platformStub) LookupSFTPServer() (string, error)                { return "", os.ErrInvalid }
func (*platformStub) ReadSystemSSHHostKey() ([]byte, error)            { return nil, os.ErrInvalid }
func (*platformStub) TailscaleHostname() string                        { return "" }
func (*platformStub) UsePlatformBridge() bool                          { return false }
func (*platformStub) CreateBridge(adapter.BridgeOptions) (adapter.BridgeSession, error) {
	return nil, os.ErrInvalid
}

type interfaceMonitorStub struct{}

func (*interfaceMonitorStub) Start() error                         { return nil }
func (*interfaceMonitorStub) Close() error                         { return nil }
func (*interfaceMonitorStub) DefaultInterface() *control.Interface { return nil }
func (*interfaceMonitorStub) OverrideAndroidVPN() bool             { return false }
func (*interfaceMonitorStub) AndroidVPNEnabled() bool              { return false }
func (*interfaceMonitorStub) RegisterCallback(tun.DefaultInterfaceUpdateCallback) *list.Element[tun.DefaultInterfaceUpdateCallback] {
	return nil
}
func (*interfaceMonitorStub) UnregisterCallback(*list.Element[tun.DefaultInterfaceUpdateCallback]) {}
func (*interfaceMonitorStub) RegisterMyInterface(string)                                           {}
func (*interfaceMonitorStub) MyInterfaces() []string                                               { return nil }
