package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/df-mc/go-nethernet/discovery"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// RunBridge forwards only connection signaling; gameplay and authentication stay
// between the actual console and the actual game server. No dummy world or Transfer.
func RunBridge(ctx context.Context, cfg Config, log *slog.Logger) error {
	if _, _, err := ParseTarget(cfg.Target); err != nil {
		return err
	}
	if cfg.DiscoveryPort < 1 || cfg.DiscoveryPort > 65535 {
		return fmt.Errorf("invalid discovery port")
	}
	iface, ip, err := LANInterface(cfg.Interface)
	if err != nil {
		return err
	}
	if !ip.IsPrivate() {
		return fmt.Errorf("select a private Wi-Fi/LAN interface")
	}
	target := "http://" + cfg.Target
	data, err := bridgeStatus(ctx, target, cfg.Name)
	if err != nil {
		return err
	}
	b, err := NewLANBridge(LANConfig{BindAddr: fmt.Sprintf("0.0.0.0:%d", cfg.DiscoveryPort), Data: data}, target, log)
	if err != nil {
		return err
	}
	defer b.Close()
	log.Info("LAN connector ready", "mode", "direct server signaling", "interface", iface.Name, "ip", ip.String(), "destination", cfg.Target)
	log.Info("Bedrock world loading and authentication are handled by the real game server")
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if next, err := bridgeStatus(ctx, target, cfg.Name); err == nil {
				b.listener.ServerData(&next)
			} else {
				log.Warn("Could not refresh server advertisement", "error", err)
			}
		}
	}
}

func bridgeStatus(ctx context.Context, target, name string) (discovery.ServerData, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target+"/v1/join", nil)
	if err != nil {
		return discovery.ServerData{}, err
	}
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return discovery.ServerData{}, fmt.Errorf("server discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return discovery.ServerData{}, fmt.Errorf("server discovery HTTP %d", resp.StatusCode)
	}
	var s struct {
		Name       string `json:"name"`
		Version    string `json:"version"`
		Protocol   int32  `json:"protocol"`
		Players    int32  `json:"players"`
		MaxPlayers int32  `json:"maxPlayers"`
		GameType   int32  `json:"gameType"`
		Online     *bool  `json:"onlineAuth"`
		SelfSigned *bool  `json:"selfSignedAuth"`
		Nonce      string `json:"nonce"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&s); err != nil {
		return discovery.ServerData{}, err
	}
	if s.Online == nil || s.SelfSigned == nil || s.Nonce == "" {
		return discovery.ServerData{}, fmt.Errorf("game server did not supply required LAN authentication policy and nonce")
	}
	version := s.Version
	if strings.HasPrefix(version, "26.") {
		version = "1." + version
	}
	return discovery.ServerData{ServerName: "Minecraft LAN Broadcaster", LevelName: name, Version: version, Protocol: s.Protocol, GameType: s.GameType, PlayerCount: s.Players, MaxPlayerCount: s.MaxPlayers, AcceptsOnlineAuth: *s.Online, AcceptsSelfSignedAuth: *s.SelfSigned, Nonce: s.Nonce, ConnectionType: 4}, nil
}
