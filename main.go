package main

import (
	"context"
	"flag"
	"local/bedrock-lan-connector/connector"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	o := connector.Config{}
	flag.StringVar(&o.Target, "target", connector.DefaultTarget, "Bedrock destination host:port")
	flag.StringVar(&o.Name, "name", "Minecraft LAN Broadcaster", "LAN world name")
	flag.StringVar(&o.Interface, "interface", "", "LAN interface (default: interface used by the IPv4 default route)")
	flag.IntVar(&o.DiscoveryPort, "discovery-port", 7551, "LAN discovery UDP port")
	flag.IntVar(&o.MediaPort, "media-port", 7552, "local connection UDP port")
	bedrock := flag.Bool("bedrock", true, "enable Bedrock LAN discovery")
	java := flag.Bool("java", false, "enable Java LAN discovery and relay")
	javaTarget := flag.String("java-target", "198.51.100.10:25565", "Java destination host:port")
	javaPort := flag.Int("java-port", 25566, "local Java relay TCP port")
	check := flag.Bool("check", false, "check target DNS and NetherNet signaling, then exit")
	debug := flag.Bool("debug", false, "include detailed protocol errors (never packet payloads)")
	flag.Parse()
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	if !*bedrock && !*java {
		log.Error("Select at least one edition")
		os.Exit(1)
	}
	if _, _, err := connector.ParseTarget(o.Target); *bedrock && err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
	if *check {
		if err := connector.CheckTarget(o.Target); err != nil {
			log.Error("Destination check failed", "error", err)
			os.Exit(1)
		}
		log.Info("Destination DNS and NetherNet signaling are reachable", "target", o.Target)
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	results := make(chan error, 2)
	count := 0
	if *bedrock {
		count++
		go func() { results <- connector.RunStaged(ctx, o, log) }()
	}
	if *java {
		count++
		go func() {
			results <- connector.RunJava(ctx, connector.JavaConfig{Target: *javaTarget, Interface: o.Interface, Port: *javaPort}, log)
		}()
	}
	var runErr error
	for i := 0; i < count; i++ {
		if err := <-results; err != nil && runErr == nil {
			runErr = err
		}
		cancel()
	}
	if runErr != nil {
		log.Error("Connector stopped", "error", runErr)
		os.Exit(1)
	}
}
