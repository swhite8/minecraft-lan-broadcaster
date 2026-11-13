package connector

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"
)

// RunStaged follows the observed reference: LAN lobby -> local HTTP signaling
// endpoint -> actual Geyser. The second join carries the console's own identity.
func RunStaged(ctx context.Context, cfg Config, log *slog.Logger) error {
	if _, _, err := ParseTarget(cfg.Target); err != nil {
		return err
	}
	_, ip, err := LANInterface(cfg.Interface)
	if err != nil {
		return err
	}
	if !ip.IsPrivate() {
		return fmt.Errorf("select a private LAN interface")
	}
	upstream, err := url.Parse("http://" + cfg.Target)
	if err != nil {
		return err
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.Request.Method == http.MethodPost {
			log.Info("Second-stage game server response", "status", resp.StatusCode)
		}
		return nil
	}
	original := proxy.Director
	proxy.Director = func(r *http.Request) { original(r); r.Host = upstream.Host }
	proxy.Transport = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, ResponseHeaderTimeout: 12 * time.Second, MaxIdleConnsPerHost: 8}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Warn("HTTP game-server connection failed", "error", err)
		http.Error(w, "Server unavailable", http.StatusBadGateway)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/join" && (len(r.URL.Path) < 9 || r.URL.Path[:9] != "/v1/join/") {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		if r.Method == http.MethodPost {
			log.Info("Second-stage HTTP join received")
		}
		r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
		if r.Method == http.MethodPost {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Invalid offer", 400)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			logOfferIdentity(log, string(raw))
		}
		proxy.ServeHTTP(w, r)
	})
	listener, err := net.Listen("tcp4", net.JoinHostPort(ip.String(), strconv.Itoa(cfg.DiscoveryPort)))
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
	defer server.Close()
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Warn("Local HTTP signaling stopped", "error", err)
		}
	}()
	cfg.TransferTarget = listener.Addr().String()
	log.Info("Second-stage local HTTP endpoint ready", "address", cfg.TransferTarget, "upstream", cfg.Target)
	return Run(ctx, cfg, log)
}
