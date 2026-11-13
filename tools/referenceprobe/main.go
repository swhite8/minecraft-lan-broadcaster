// referenceprobe observes a user-started LAN helper without game-account login.
// It prints packet types and Transfer fields, never login tokens or skin data.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/df-mc/go-nethernet"
	"github.com/df-mc/go-nethernet/discovery"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
	"math/rand/v2"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type lanNetwork struct {
	minecraft.Network
	id string
}

func (n lanNetwork) DialContext(ctx context.Context, _ string) (net.Conn, error) {
	return n.Network.DialContext(ctx, n.id)
}

func main() {
	match := flag.String("match", "", "case-insensitive advertised world substring")
	flag.Parse()
	socket, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		panic(err)
	}
	defer socket.Close()
	_ = socket.SetReadDeadline(time.Now().Add(4 * time.Second))
	_, err = socket.WriteToUDP(discovery.Marshal(&discovery.RequestPacket{}, rand.Uint64()), &net.UDPAddr{IP: net.IPv4bcast, Port: 7551})
	if err != nil {
		panic(err)
	}
	var id uint64
	var data discovery.ServerData
	var peer net.IP
	for {
		buf := make([]byte, 65535)
		n, from, e := socket.ReadFromUDP(buf)
		if e != nil {
			break
		}
		p, sender, e := discovery.Unmarshal(buf[:n])
		if e != nil {
			continue
		}
		response, ok := p.(*discovery.ResponsePacket)
		if !ok {
			continue
		}
		var d discovery.ServerData
		if d.UnmarshalBinary(response.ApplicationData) != nil {
			continue
		}
		fmt.Printf("Found: %q at %s version=%s protocol=%d online=%t self_signed=%t\n", d.LevelName, from.IP, d.Version, d.Protocol, d.AcceptsOnlineAuth, d.AcceptsSelfSignedAuth)
		if *match == "" && strings.Contains(d.LevelName, "Minecraft LAN Broadcaster") {
			continue
		}
		if *match != "" && !strings.Contains(strings.ToLower(d.LevelName), strings.ToLower(*match)) {
			continue
		}
		id = sender
		data = d
		peer = from.IP
		break
	}
	if id == 0 {
		fmt.Println("No matching reference helper is broadcasting.")
		os.Exit(2)
	}
	lan, err := (discovery.ListenConfig{}).Listen("0.0.0.0:0")
	if err != nil {
		panic(err)
	}
	defer lan.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	for {
		if _, ok := lan.Responses()[id]; ok {
			break
		}
		select {
		case <-ctx.Done():
			panic("reference discovery timed out")
		case <-time.After(100 * time.Millisecond):
		}
	}
	var chunks atomic.Int32
	seen := make(chan struct{}, 1)
	d := minecraft.Dialer{DisablePortFollowing: true, ClientData: login.ClientData{Nonce: data.Nonce}, PacketFunc: func(h packet.Header, payload []byte, src, dst net.Addr) {
		direction := "client -> helper"
		if strings.HasPrefix(src.String(), strconv.FormatUint(id, 10)+" ") {
			direction = "helper -> client"
		}
		label := fmt.Sprintf("id=%d", h.PacketID)
		pool := packet.NewServerPool()
		if direction == "client -> helper" {
			pool = packet.NewClientPool()
		}
		if factory, ok := pool[h.PacketID]; ok {
			label = fmt.Sprintf("%T", factory())
		}
		if h.PacketID == packet.IDLevelChunk {
			chunks.Add(1)
			var p packet.LevelChunk
			p.Marshal(protocol.NewReader(bytes.NewBuffer(payload), 0, true))
			fmt.Printf("REFERENCE CHUNK position=%v subchunks=%d cache=%t length=%d prefix=%x\n", p.Position, p.SubChunkCount, p.CacheEnabled, len(p.RawPayload), p.RawPayload[:min(96, len(p.RawPayload))])
			_ = os.WriteFile("/tmp/reference-lobby-chunk.bin", p.RawPayload, 0600)
		}
		if direction == "helper -> client" && h.PacketID == packet.IDStartGame {
			var p packet.StartGame
			p.Marshal(protocol.NewReader(bytes.NewBuffer(payload), 0, true))
			info := map[string]any{"entity": p.EntityRuntimeID, "position": p.PlayerPosition, "spawn": p.WorldSpawn, "gamemode": p.PlayerGameMode, "worldmode": p.WorldGameMode, "generator": p.Generator, "client_generation": p.ClientSideGeneration, "hash_ids": p.UseBlockNetworkIDHashes, "base_version": p.BaseGameVersion, "game_version": p.GameVersion, "education": p.EducationFeaturesEnabled, "movement": p.PlayerMovementSettings, "permissions": p.PlayerPermissions, "xbl_broadcast": p.XBLBroadcastMode, "property": p.PropertyData}
			_ = info
			b, _ := json.Marshal(p)
			fmt.Println("REFERENCE STARTGAME", string(b))
		}
		if direction == "helper -> client" && h.PacketID == packet.IDItemRegistry {
			var p packet.ItemRegistry
			p.Marshal(protocol.NewReader(bytes.NewBuffer(payload), 0, true))
			fmt.Println("REFERENCE ITEM COUNT", len(p.Items))
		}
		names := map[uint32]string{packet.IDStartGame: "StartGame", packet.IDPlayStatus: "PlayStatus", packet.IDResourcePacksInfo: "ResourcePacksInfo", packet.IDResourcePackStack: "ResourcePackStack", packet.IDResourcePackClientResponse: "ResourcePackClientResponse", packet.IDTransfer: "Transfer", packet.IDItemRegistry: "ItemRegistry", packet.IDLevelChunk: "LevelChunk", packet.IDRequestChunkRadius: "RequestChunkRadius", packet.IDSetLocalPlayerAsInitialised: "SetLocalPlayerAsInitialised", packet.IDChunkRadiusUpdated: "ChunkRadiusUpdated"}
		if name, ok := names[h.PacketID]; ok {
			label = name
		}
		if h.PacketID != packet.IDLevelChunk {
			fmt.Printf("%s %s bytes=%d sha=%x\n", direction, label, len(payload), sha256.Sum256(payload))
			if direction == "helper -> client" && (h.PacketID == packet.IDChunkRadiusUpdated || h.PacketID == packet.IDPlayStatus || h.PacketID == packet.IDResourcePacksInfo || h.PacketID == packet.IDResourcePackStack) {
				if factory, ok := pool[h.PacketID]; ok {
					pk := factory()
					pk.Marshal(protocol.NewReader(bytes.NewBuffer(payload), 0, true))
					b, _ := json.Marshal(pk)
					fmt.Println("DETAIL", string(b))
				}
			}
		}
		if h.PacketID == packet.IDTransfer {
			var p packet.Transfer
			p.Marshal(protocol.NewReader(bytes.NewBuffer(payload), 0, true))
			fmt.Println("REFERENCE CHUNK COUNT", chunks.Load())
			fmt.Printf("REFERENCE TRANSFER target=%s:%d reload_world=%t\n", p.Address, p.Port, p.ReloadWorld)
			select {
			case seen <- struct{}{}:
			default:
			}
		}
	}}
	n := lanNetwork{Network: minecraft.NetherNet{Signaling: lan, Dialer: nethernet.Dialer{AllowIdentitylessServer: true}}, id: strconv.FormatUint(id, 10)}
	c, err := d.DialContextNetwork(ctx, n, net.JoinHostPort(peer.String(), "7551"))
	if c != nil {
		defer c.Close()
		go func() { _ = c.DoSpawnContext(ctx) }()
		go func() {
			for {
				if _, e := c.ReadPacket(); e != nil {
					return
				}
			}
		}()
	}
	if err != nil {
		fmt.Println("Reference login result:", err)
		return
	}
	select {
	case <-seen:
		fmt.Println("Reference transfer captured.")
	case <-ctx.Done():
		fmt.Println("Reference observation timed out.")
	}
}
