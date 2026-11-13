package connector

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/net/ipv4"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// JavaConfig advertises a local TCP relay because vanilla Java LAN discovery
// combines the announcement sender's address with the advertised TCP port.
type JavaConfig struct {
	Target, Interface string
	Port              int
}

func RunJava(ctx context.Context, cfg JavaConfig, log *slog.Logger) error {
	if _, _, err := ParseTarget(cfg.Target); err != nil {
		return err
	}
	iface, ip, err := LANInterface(cfg.Interface)
	if err != nil {
		return err
	}
	if !ip.IsPrivate() && !ip.IsLinkLocalUnicast() {
		return errors.New("Java requires a private LAN interface")
	}
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: ip, Port: cfg.Port})
	if err != nil {
		return fmt.Errorf("Java relay port: %w", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	host, targetPort, _ := ParseTarget(cfg.Target)
	if targetPort == uint16(port) {
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
		if err != nil {
			return err
		}
		for _, targetIP := range ips {
			if targetIP.Equal(ip) {
				return errors.New("Java destination points back to the phone's own relay")
			}
		}
	}
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip})
	if err != nil {
		return err
	}
	defer udp.Close()
	mc := ipv4.NewPacketConn(udp)
	if err := mc.SetMulticastInterface(iface); err != nil {
		return err
	}
	if err := mc.SetMulticastTTL(1); err != nil {
		return err
	}
	payload := []byte("[MOTD]Minecraft LAN Broadcaster - Java[/MOTD][AD]" + strconv.Itoa(port) + "[/AD]")
	destination := &net.UDPAddr{IP: net.IPv4(224, 0, 2, 60), Port: 4445}
	send := func() error { _, err := udp.WriteToUDP(payload, destination); return err }
	if err := send(); err != nil {
		return fmt.Errorf("Java multicast: %w", err)
	}
	var mu sync.Mutex
	active := map[net.Conn]bool{}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	defer close(stop)
	closeAll := func() {
		listener.Close()
		mu.Lock()
		defer mu.Unlock()
		for c := range active {
			c.Close()
		}
	}
	defer func() { closeAll(); wg.Wait() }()
	go func() {
		select {
		case <-ctx.Done():
			closeAll()
		case <-stop:
		}
	}()
	go func() {
		tick := time.NewTicker(1500 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-tick.C:
				if err := send(); err != nil && !strings.Contains(err.Error(), "closed") {
					log.Warn("Java announcement failed", "error", err)
				}
			}
		}
	}()
	log.Info("Java LAN broadcaster ready", "interface", iface.Name, "ip", ip.String(), "local_port", port, "destination", cfg.Target)
	log.Info("Java players must keep this app running while playing")
	limit := make(chan struct{}, 32)
	for {
		local, err := listener.AcceptTCP()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case limit <- struct{}{}:
		default:
			local.Close()
			continue
		}
		mu.Lock()
		active[local] = true
		mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-limit; mu.Lock(); delete(active, local); mu.Unlock(); local.Close() }()
			upstream, err := (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, "tcp", cfg.Target)
			if err != nil {
				log.Warn("Java server connection failed", "error", err)
				return
			}
			mu.Lock()
			if ctx.Err() != nil {
				mu.Unlock()
				upstream.Close()
				return
			}
			active[upstream] = true
			mu.Unlock()
			defer func() { mu.Lock(); delete(active, upstream); mu.Unlock(); upstream.Close() }()
			log.Info("Java connection forwarding")
			copied := make(chan struct{}, 2)
			pipe := func(dst, src net.Conn) {
				_, _ = io.Copy(dst, src)
				if c, ok := dst.(*net.TCPConn); ok {
					_ = c.CloseWrite()
				}
				copied <- struct{}{}
			}
			go pipe(upstream, local)
			go pipe(local, upstream)
			select {
			case <-ctx.Done():
				local.Close()
				upstream.Close()
			case <-copied:
				local.Close()
				upstream.Close()
			}
			<-copied
		}()
	}
}
