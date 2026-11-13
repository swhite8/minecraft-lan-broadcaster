package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/df-mc/go-nethernet"
	"github.com/df-mc/go-nethernet/discovery"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
	"io"
	"local/bedrock-lan-connector/connector"
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// Android hardware smoke test. Start the app with the Home network target first.
func TestAndroidDeviceTransfer(t *testing.T) {

	phone := os.Getenv("CONNECTOR_PHONE")
	if phone == "" {
		t.Skip("set CONNECTOR_PHONE to the Android phone's LAN IPv4 address")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	ip := net.ParseIP(phone)
	if ip == nil {
		t.Fatal("invalid phone IP")
	}
	lan, err := (discovery.ListenConfig{BroadcastAddress: &net.UDPAddr{IP: ip, Port: 7551}}).Listen("0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lan.Close()
	var id uint64
	for id == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("phone discovery timed out")
		case <-time.After(100 * time.Millisecond):
		}
		for candidate, raw := range lan.Responses() {
			var d discovery.ServerData
			if d.UnmarshalBinary(raw) == nil && d.LevelName == "Minecraft LAN Broadcaster - Phone" {
				id = candidate
				break
			}
		}
	}
	var chunks atomic.Int32
	got := make(chan packet.Transfer, 1)
	done := make(chan error, 1)
	go func() {
		d := minecraft.Dialer{DisablePortFollowing: true, PacketFunc: func(h packet.Header, payload []byte, _, _ net.Addr) {
			if h.PacketID == packet.IDLevelChunk {
				chunks.Add(1)
			}
			if h.PacketID == packet.IDTransfer {
				var p packet.Transfer
				p.Marshal(protocol.NewReader(bytes.NewBuffer(payload), 0, true))
				select {
				case got <- p:
				default:
				}
			}
		}}
		n := testLANNetwork{Network: minecraft.NetherNet{Signaling: lan, Dialer: nethernet.Dialer{AllowIdentitylessServer: true}}, id: strconv.FormatUint(id, 10)}
		c, err := d.DialContextNetwork(ctx, n, net.JoinHostPort(phone, "7551"))
		if c != nil {
			defer c.Close()
			if e := c.DoSpawnTimeout(12 * time.Second); e != nil {
				done <- e
				return
			}
			for {
				pk, e := c.ReadPacket()
				if e != nil {
					done <- e
					return
				}
				if _, ok := pk.(*packet.Transfer); ok {
					return
				}
			}
		}
		done <- err
	}()
	select {
	case p := <-got:
		if chunks.Load() == 0 {
			t.Fatal("transfer arrived without loading-area chunks")
		}
		if p.Address != phone || p.Port != 7551 {
			t.Fatalf("unexpected phone transfer: %+v", p)
		}
		t.Logf("Physical Android phone completed NetherNet login and sent Transfer(%s,%d)", p.Address, p.Port)
	case err := <-done:
		t.Fatalf("phone connection failed: %v", err)
	case <-ctx.Done():
		t.Fatal("phone transfer timed out")
	}
}

// A real Java LAN client combines the multicast sender IP with [AD]port.
// Verify the phone announces that endpoint and proxies an actual Paper status.
func TestAndroidJavaBroadcastAndRelay(t *testing.T) {
	phone := os.Getenv("CONNECTOR_PHONE")
	if phone == "" {
		t.Skip("set CONNECTOR_PHONE")
	}
	iface, _, err := connector.LANInterface("")
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenMulticastUDP("udp4", iface, &net.UDPAddr{IP: net.IPv4(224, 0, 2, 60), Port: 4445})
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	_ = udp.SetReadDeadline(time.Now().Add(7 * time.Second))
	buf := make([]byte, 2048)
	for {
		n, from, err := udp.ReadFromUDP(buf)
		if err != nil {
			t.Fatal(err)
		}
		if from.IP.String() != phone {
			continue
		}
		got := string(buf[:n])
		if got != "[MOTD]Minecraft LAN Broadcaster - Java[/MOTD][AD]25565[/AD]" {
			t.Fatalf("unexpected announcement: %s", got)
		}
		break
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort(phone, "25565"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	vi := func(n int) []byte {
		out := []byte{}
		for {
			b := byte(n & 127)
			n >>= 7
			if n > 0 {
				b |= 128
			}
			out = append(out, b)
			if n == 0 {
				return out
			}
		}
	}
	host := []byte(phone)
	p := append([]byte{0}, vi(777)...)
	p = append(p, vi(len(host))...)
	p = append(p, host...)
	p = append(p, 0x63, 0xdd, 1)
	req := append(vi(len(p)), p...)
	req = append(req, 1, 0)
	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}
	rv := func() int {
		n := 0
		for i := 0; i < 5; i++ {
			var b [1]byte
			if _, err := io.ReadFull(c, b[:]); err != nil {
				t.Fatal(err)
			}
			n |= int(b[0]&127) << (7 * i)
			if b[0] < 128 {
				return n
			}
		}
		t.Fatal("invalid varint")
		return 0
	}
	rv()
	if rv() != 0 {
		t.Fatal("unexpected status packet")
	}
	n := rv()
	if n < 1 || n > 1048576 {
		t.Fatal("bad status length")
	}
	raw := make([]byte, n)
	if _, err := io.ReadFull(c, raw); err != nil {
		t.Fatal(err)
	}
	var status struct {
		Version struct {
			Name string `json:"name"`
		} `json:"version"`
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatal(err)
	}
	if status.Version.Name != "Paper 26.3" {
		t.Fatalf("unexpected server: %s", raw)
	}
	t.Log("Phone multicast announcement received; Java status traversed phone relay to Paper 26.3")
}

func TestAndroidBridgeAdvertisement(t *testing.T) {
	phone := os.Getenv("CONNECTOR_PHONE")
	if phone == "" {
		t.Skip("set CONNECTOR_PHONE")
	}
	l, err := (discovery.ListenConfig{BroadcastAddress: &net.UDPAddr{IP: net.ParseIP(phone), Port: 7551}}).Listen("0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	deadline := time.After(8 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("phone discovery timed out")
		case <-time.After(100 * time.Millisecond):
			for _, raw := range l.Responses() {
				var d discovery.ServerData
				if d.UnmarshalBinary(raw) != nil || d.LevelName != "Minecraft LAN Broadcaster - Phone" {
					continue
				}
				if !d.AcceptsOnlineAuth || !d.AcceptsSelfSignedAuth || d.Nonce == "" || d.Protocol != 2225 {
					t.Fatalf("advertisement does not preserve Geyser policy: %+v", d)
				}
				t.Logf("Phone advertises staged LAN lobby protocol %d with online/self-signed LAN authentication", d.Protocol)
				return
			}
		}
	}
}
