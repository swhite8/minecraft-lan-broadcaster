package connector

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/df-mc/go-nethernet"
	"github.com/df-mc/go-nethernet/discovery"
	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
	"github.com/wlynxg/anet"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const DefaultTarget = "198.51.100.10:19132"

type Config struct {
	Target, Name, Interface  string
	TransferTarget           string
	DiscoveryPort, MediaPort int
}

func ParseTarget(target string) (string, uint16, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil || host == "" || strings.ContainsAny(host, "/\r\n\t ") {
		return "", 0, fmt.Errorf("target must be a hostname or IP and port, for example %s", DefaultTarget)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", 0, errors.New("target port must be between 1 and 65535")
	}
	return host, uint16(n), nil
}

func LANInterface(name string) (*net.Interface, net.IP, error) {
	var routeIP net.IP
	if name == "" {
		// UDP connect selects a route without sending a packet.
		c, err := net.Dial("udp4", "1.1.1.1:80")
		if err != nil {
			return nil, nil, fmt.Errorf("select LAN interface: %w; use -interface", err)
		}
		routeIP = c.LocalAddr().(*net.UDPAddr).IP
		_ = c.Close()
	}
	ifaces, err := anet.Interfaces()
	if err != nil {
		return nil, nil, err
	}
	for _, iface := range ifaces {
		if name != "" && iface.Name != name {
			continue
		}
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := anet.InterfaceAddrsByInterface(&iface)
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil || ip.To4() == nil {
				continue
			}
			if name != "" || ip.Equal(routeIP) {
				return &iface, ip.To4(), nil
			}
		}
	}
	return nil, nil, errors.New("no usable IPv4 LAN interface found; pass -interface")
}

// advertisedNetwork preserves a complete advertisement when Gophertunnel refreshes
// its legacy-format pong. In particular, protocol/version, auth flags and nonce
// must not be lost by the library's generic pong-to-discovery conversion.
type advertisedNetwork struct {
	minecraft.Network
	lan  *discovery.Listener
	data discovery.ServerData
}

func (n advertisedNetwork) Listen(addr string) (minecraft.NetworkListener, error) {
	l, err := n.Network.Listen(addr)
	if err != nil {
		return nil, err
	}
	return advertisedListener{NetworkListener: l, lan: n.lan, data: n.data}, nil
}

type advertisedListener struct {
	minecraft.NetworkListener
	lan  *discovery.Listener
	data discovery.ServerData
}

func (l advertisedListener) PongData(_ []byte) { d := l.data; l.lan.ServerData(&d) }

func Run(ctx context.Context, o Config, log *slog.Logger) error {
	host, port, err := ParseTarget(o.Target)
	if o.TransferTarget != "" {
		host, port, err = ParseTarget(o.TransferTarget)
	}
	if err != nil {
		return err
	}
	if o.DiscoveryPort < 1 || o.DiscoveryPort > 65535 || o.MediaPort < 1 || o.MediaPort > 65535 || o.DiscoveryPort == o.MediaPort {
		return errors.New("discovery and media ports must be distinct and between 1 and 65535")
	}
	if o.Name == "" || len(o.Name) > 100 || strings.ContainsAny(o.Name, ";\r\n") {
		return errors.New("name must be 1-100 bytes without semicolons or newlines")
	}
	iface, ip, err := LANInterface(o.Interface)
	if err != nil {
		return err
	}
	if !ip.IsPrivate() && !ip.IsLinkLocalUnicast() {
		return errors.New("selected interface is not a private LAN; use -interface for your home network")
	}
	media, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip, Port: o.MediaPort})
	if err != nil {
		return fmt.Errorf("bind connection UDP port: %w", err)
	}
	mux := ice.NewUDPMuxDefault(ice.UDPMuxParams{UDPConn: media})
	defer mux.Close()
	var settings webrtc.SettingEngine
	settings.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	settings.SetInterfaceFilter(func(name string) bool { return name == iface.Name })
	settings.SetIPFilter(func(candidate net.IP) bool { return candidate.Equal(ip) })
	settings.SetICEUDPMux(mux)
	api := webrtc.NewAPI(webrtc.WithSettingEngine(settings))
	lan, err := (discovery.ListenConfig{Log: log}).Listen(fmt.Sprintf("0.0.0.0:%d", o.DiscoveryPort))
	if err != nil {
		return fmt.Errorf("bind discovery UDP port (close Minecraft Bedrock or another LAN helper on this computer): %w", err)
	}
	defer lan.Close()
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	data := discovery.ServerData{
		ServerName: "Minecraft LAN Broadcaster", LevelName: o.Name,
		Protocol: protocol.CurrentProtocol, Version: protocol.CurrentVersion,
		GameType: discovery.GameTypeSurvival, MaxPlayerCount: 8,
		AcceptsSelfSignedAuth: true, AcceptsOnlineAuth: true,
		Nonce: hex.EncodeToString(nonce), ConnectionType: 4,
	}
	lan.ServerData(&data)
	network := advertisedNetwork{
		Network: minecraft.NetherNet{Signaling: plainLANSignaling{lan}, Log: log,
			// This is a LAN-only transfer bootstrap, not the authenticated game
			// server. Also accept offline clients without an SDP identity.
			ListenConfig: nethernet.ListenConfig{API: api, Log: log, AllowAnonymous: true}},
		lan: lan, data: data,
	}
	var clients sync.Map
	listener, err := (minecraft.ListenConfig{
		AcceptedProtocols: []minecraft.Protocol{lobbyProtocol{minecraft.DefaultProtocol}},
		PacketFunc: func(h packet.Header, _ []byte, src, dst net.Addr) {
			if h.PacketID == packet.IDRequestChunkRadius {
				log.Info("Client requested loading area")
				if value, ok := clients.Load(src.String()); ok {
					if err := sendLoadingArea(value.(*lobbySession).conn, log); err != nil {
						log.Warn("Loading marker failed", "error", err)
					} else {
						select {
						case value.(*lobbySession).loading <- struct{}{}:
						default:
						}
					}
				}
			}
			if h.PacketID == packet.IDSetLocalPlayerAsInitialised {
				log.Info("Client acknowledged local player initialization")
			}
		},
		ErrorLog: log, AuthenticationDisabled: true,
		MaximumPlayers: 8, LoginTimeout: 10 * time.Second,
		MaxDecompressedLen: 4 * 1024 * 1024,
		StatusProvider:     minecraft.NewStatusProvider("Minecraft LAN Broadcaster", o.Name),
	}).ListenNetwork(network, "")
	if err != nil {
		return fmt.Errorf("start bootstrap listener: %w", err)
	}
	defer listener.Close()
	go func() { <-ctx.Done(); _ = listener.Close() }()
	log.Info("LAN connector ready", "world", o.Name, "interface", iface.Name, "ip", ip.String(), "discovery_udp", o.DiscoveryPort, "connection_udp", o.MediaPort)
	log.Info("Select this LAN world on your Switch, Xbox or PlayStation", "destination", o.Target, "bedrock", protocol.CurrentVersion, "protocol", protocol.CurrentProtocol)
	log.Info("No account sign-in, ads or analytics. Do not forward connector ports in your router. Stop with Ctrl+C.")
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		session := &lobbySession{conn: conn.(*minecraft.Conn), loading: make(chan struct{}, 1)}
		clients.Store(conn.RemoteAddr().String(), session)
		go func() { defer clients.Delete(session.conn.RemoteAddr().String()); transfer(session, host, port, log) }()
	}
}

type lobbySession struct {
	conn    *minecraft.Conn
	loading chan struct{}
}

func transfer(session *lobbySession, host string, port uint16, log *slog.Logger) {
	conn := session.conn
	defer conn.Close()
	log.Info("Local login complete; initializing transfer session")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	initialized := make(chan error, 1)
	go func() {
		initialized <- conn.StartGameContext(ctx, minecraft.GameData{
			WorldName: "Minecraft LAN Broadcaster", EntityUniqueID: 1, EntityRuntimeID: 1,
			PlayerGameMode: 1, WorldGameMode: 1, PlayerPermissions: 1, PropertyData: map[string]any{},
		})
	}()
	timeout := time.NewTimer(15 * time.Second)
	defer timeout.Stop()
	var fallback <-chan time.Time
	var fallbackTimer *time.Timer
	defer func() {
		if fallbackTimer != nil {
			fallbackTimer.Stop()
		}
	}()
waiting:
	for {
		select {
		case err := <-initialized:
			if err != nil {
				log.Warn("Transfer session initialization failed", "error", err)
				return
			}
			log.Info("Client finished transfer-session initialization; sending destination")
			break waiting
		case <-session.loading:
			// Some console clients never acknowledge this empty lobby. Allow its
			// chunk, radius and spawn packets to flush before issuing the transfer.
			if fallbackTimer == nil {
				fallbackTimer = time.NewTimer(2 * time.Second)
				fallback = fallbackTimer.C
			}
		case <-fallback:
			log.Info("Loading packets delivered; sending transfer without initialization acknowledgement")
			break waiting
		case <-timeout.C:
			log.Warn("Transfer session received no loading request before timeout")
			return
		}
	}

	if err := conn.WritePacket(&packet.Transfer{Address: host, Port: port}); err != nil {
		log.Warn("Could not send transfer", "error", err)
		return
	}
	if err := conn.Flush(); err != nil {
		log.Warn("Could not flush transfer", "error", err)
		return
	}
	log.Info("Transfer sent; check the console for destination join", "target", net.JoinHostPort(host, strconv.Itoa(int(port))))
	// Keep the transport alive briefly while the client handles the transfer.
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	for {
		if _, err := conn.ReadPacket(); err != nil {
			log.Info("Transfer connection closed", "detail", err.Error())
			return
		}
	}
}

// Vanilla clients need loaded chunks before acknowledging initialization. The
// transfer lobby is empty and never saved; no real world data is hosted here.
func sendLoadingArea(conn *minecraft.Conn, log *slog.Logger) error {
	payload := make([]byte, 258)
	payload[0] = 0x0a
	if err := conn.WritePacket(&packet.LevelChunk{Position: protocol.ChunkPos{0, 0}, SubChunkCount: 0, RawPayload: payload}); err != nil {
		return err
	}
	log.Info("Reference-compatible loading marker sent")
	return nil
}

// Match the observed local lobby metadata without weakening the real server.
type lobbyProtocol struct{ minecraft.Protocol }

func (p lobbyProtocol) ConvertFromLatest(pk packet.Packet, c *minecraft.Conn) []packet.Packet {
	if stack, ok := pk.(*packet.ResourcePackStack); ok {
		stack.BaseGameVersion = "*"
		stack.Experiments = nil
	}
	if game, ok := pk.(*packet.StartGame); ok {
		game.GameVersion = ""
		game.BaseGameVersion = ""
	}
	return p.Protocol.ConvertFromLatest(pk, c)
}

// Vanilla LAN does not use the authenticated identity assertion required for
// HTTP joins. Only the temporary LAN lobby omits it; Geyser remains untouched.
type plainLANSignaling struct{ *discovery.Listener }

func (s plainLANSignaling) Signal(ctx context.Context, signal *nethernet.Signal) error {
	if signal.Type == nethernet.SignalTypeAnswer {
		clone := *signal
		var lines []string
		for _, line := range strings.Split(signal.Data, "\n") {
			if !strings.HasPrefix(line, "a=identity:") {
				lines = append(lines, line)
			}
		}
		clone.Data = strings.Join(lines, "\n")
		return s.Listener.Signal(ctx, &clone)
	}
	return s.Listener.Signal(ctx, signal)
}

func CheckTarget(target string) error {
	host, _, err := ParseTarget(target)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := net.DefaultResolver.LookupHost(ctx, host); err != nil {
		return fmt.Errorf("DNS: %w", err)
	}
	_, err = (minecraft.NetherNet{}).PingContext(ctx, "http://"+target)
	if err != nil {
		return fmt.Errorf("NetherNet signaling: %w", err)
	}
	return nil
}
