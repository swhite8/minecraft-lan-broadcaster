package connector

import (
	"context"
	"github.com/df-mc/go-nethernet"
	"github.com/df-mc/go-nethernet/discovery"
	"github.com/df-mc/go-nethernet/endpoint"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestLANHTTPBridgeTransport(t *testing.T) {
	if os.Getenv("CONNECTOR_INTEGRATION") != "1" {
		t.Skip("set CONNECTOR_INTEGRATION=1")
	}
	_, ip, err := LANInterface("")
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := endpoint.Serve(net.JoinHostPort(ip.String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	server, err := (nethernet.ListenConfig{AllowAnonymous: true, Log: log}).Listen(h)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	free, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip})
	if err != nil {
		t.Fatal(err)
	}
	port := free.LocalAddr().(*net.UDPAddr).Port
	free.Close()
	b, err := NewLANBridge(LANConfig{BindAddr: net.JoinHostPort(ip.String(), strconv.Itoa(port)), Data: discovery.ServerData{ServerName: "Test", LevelName: "Bridge Test", Version: "1.26.62", Protocol: 2225, AcceptsSelfSignedAuth: true, ConnectionType: 4, Nonce: "test"}}, "http://"+h.Addr().String(), log)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	lan, err := (discovery.ListenConfig{Log: log, BroadcastAddress: &net.UDPAddr{IP: ip, Port: port}}).Listen(net.JoinHostPort(ip.String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer lan.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for len(lan.Responses()) == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("no discovery response")
		case <-time.After(100 * time.Millisecond):
		}
	}
	echo := make(chan error, 1)
	go func() {
		c, err := server.Accept()
		if err != nil {
			echo <- err
			return
		}
		defer c.Close()
		buf := make([]byte, 32)
		n, err := c.Read(buf)
		if err == nil {
			_, err = c.Write(buf[:n])
		}
		echo <- err
	}()
	c, err := (nethernet.Dialer{Log: log}).DialContext(ctx, b.NetworkID(), lan)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("bridge-test")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "bridge-test" {
		t.Fatalf("bad echo %q", buf[:n])
	}
	if err := <-echo; err != nil {
		t.Fatal(err)
	}
	t.Log("LAN discovery -> trickled ICE gathering -> HTTP SDP -> direct WebRTC data verified")
}
