package main

import (
	"bytes"
	"context"
	"fmt"
	"local/bedrock-lan-connector/connector"
	"log/slog"
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/df-mc/go-nethernet"
	"github.com/df-mc/go-nethernet/discovery"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Keep the test client's Minecraft login ServerAddress as host:port while the
// NetherNet transport itself addresses the discovered numeric network ID.
type testLANNetwork struct {
	minecraft.Network
	id string
}

func (n testLANNetwork) DialContext(ctx context.Context, _ string) (net.Conn, error) {
	return n.Network.DialContext(ctx, n.id)
}

func TestTargetValidation(t *testing.T) {
	for _, s := range []string{"", "host", "host:0", "host:65536", "http://host:19132", "host/path:19132", " :19132"} {
		if _, _, err := connector.ParseTarget(s); err == nil {
			t.Errorf("accepted invalid target %q", s)
		}
	}
	h, p, err := connector.ParseTarget(connector.DefaultTarget)
	if err != nil || h != "198.51.100.10" || p != 19132 {
		t.Fatalf("bad default target: %q %d %v", h, p, err)
	}
}

// Run with CONNECTOR_INTEGRATION=1. Uses two temporary UDP ports on the actual
// LAN interface, never the production Geyser ports or a Microsoft account.
func TestLANDiscoveryAndTransfer(t *testing.T) { testLANDiscoveryAndTransfer(t, false) }

func TestLANTransferWithoutInitializationAcknowledgement(t *testing.T) {
	testLANDiscoveryAndTransfer(t, true)
}

type omitInitializationProtocol struct{ minecraft.Protocol }

func (p omitInitializationProtocol) ConvertFromLatest(pk packet.Packet, c *minecraft.Conn) []packet.Packet {
	if _, ok := pk.(*packet.SetLocalPlayerAsInitialised); ok {
		return nil
	}
	return p.Protocol.ConvertFromLatest(pk, c)
}

func testLANDiscoveryAndTransfer(t *testing.T, omitInitialization bool) {
	if os.Getenv("CONNECTOR_INTEGRATION") != "1" {
		t.Skip("set CONNECTOR_INTEGRATION=1 to allow local network integration test")
	}
	iface, ip, err := connector.LANInterface("")
	if err != nil {
		t.Fatal(err)
	}
	freePort := func() int {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		return c.LocalAddr().(*net.UDPAddr).Port
	}
	discoveryPort, mediaPort := freePort(), freePort()
	for discoveryPort == mediaPort {
		mediaPort = freePort()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	done := make(chan error, 1)
	go func() {
		done <- connector.Run(ctx, connector.Config{Target: connector.DefaultTarget, Name: "Integration Test", Interface: iface.Name, DiscoveryPort: discoveryPort, MediaPort: mediaPort}, log)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("listener did not stop")
		}
	})
	clientLAN, err := (discovery.ListenConfig{Log: log, BroadcastAddress: &net.UDPAddr{IP: ip, Port: discoveryPort}}).Listen(net.JoinHostPort(ip.String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer clientLAN.Close()
	var id uint64
	var data discovery.ServerData
	for id == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("discovery timed out")
		case <-time.After(100 * time.Millisecond):
		}
		for candidate, raw := range clientLAN.Responses() {
			if err := data.UnmarshalBinary(raw); err != nil {
				t.Fatal(err)
			}
			if data.LevelName == "Integration Test" {
				id = candidate
				break
			}
		}
	}
	if data.Protocol != protocol.CurrentProtocol || data.Version != protocol.CurrentVersion || data.Nonce == "" || !data.AcceptsSelfSignedAuth || data.ConnectionType != 4 {
		t.Fatalf("incomplete advertisement: %+v", data)
	}
	t.Logf("discovery OK: %s, version %s protocol %d", data.LevelName, data.Version, data.Protocol)
	var chunks atomic.Int32
	gotTransfer := make(chan packet.Transfer, 1)
	dialDone := make(chan error, 1)
	dialCtx, dialCancel := context.WithCancel(ctx)
	defer dialCancel()
	go func() {
		d := minecraft.Dialer{DisablePortFollowing: true, ClientData: login.ClientData{ServerAddress: net.JoinHostPort(ip.String(), strconv.Itoa(discoveryPort)), Nonce: data.Nonce}, PacketFunc: func(h packet.Header, payload []byte, _, _ net.Addr) {
			if h.PacketID == packet.IDLevelChunk {
				chunks.Add(1)
			}
			if h.PacketID == packet.IDTransfer {
				var p packet.Transfer
				p.Marshal(protocol.NewReader(bytes.NewBuffer(payload), 0, true))
				select {
				case gotTransfer <- p:
				default:
				}
			}
		}}
		if omitInitialization {
			d.Protocol = omitInitializationProtocol{minecraft.DefaultProtocol}
		}
		n := testLANNetwork{Network: minecraft.NetherNet{Signaling: clientLAN, Log: log, Dialer: nethernet.Dialer{AllowIdentitylessServer: true}}, id: strconv.FormatUint(id, 10)}
		c, err := d.DialContextNetwork(dialCtx, n, net.JoinHostPort(ip.String(), strconv.Itoa(discoveryPort)))
		if c != nil {
			defer c.Close()
			if e := c.DoSpawnTimeout(12 * time.Second); e != nil {
				dialDone <- e
				return
			}
			for {
				pk, e := c.ReadPacket()
				if e != nil {
					dialDone <- e
					return
				}
				if _, ok := pk.(*packet.Transfer); ok {
					return
				}
			}
		}
		dialDone <- err
	}()
	select {
	case p := <-gotTransfer:
		if chunks.Load() == 0 {
			t.Fatal("transfer arrived without loading-area chunks")
		}
		if p.Address != "198.51.100.10" || p.Port != 19132 {
			t.Fatalf("wrong transfer: %+v", p)
		}
		t.Log(fmt.Sprintf("NetherNet login and Transfer received: %s:%d", p.Address, p.Port))
	case err := <-dialDone:
		t.Fatalf("client exited before transfer: %v", err)
	case <-ctx.Done():
		t.Fatal("transfer timed out")
	}
	dialCancel()
}
