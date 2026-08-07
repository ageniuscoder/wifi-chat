package discovery

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

const (
	broadcastPort     = 19540
	broadcastInterval = 3 * time.Second
	peerTimeout       = 10 * time.Second
	maxPacketSize     = 1024
)

// Announcement is the UDP broadcast payload
type Announcement struct {
	NodeID string `json:"node_id"`
	Port   int    `json:"port"`
}

// Discovery handles automatic peer finding via UDP broadcast on the LAN
type Discovery struct {
	nodeID string
	port   int

	mu    sync.RWMutex
	peers map[string]time.Time // nodeID -> last seen

	OnPeerFound func(nodeID string, addr string) // callback when new peer discovered

	stop chan struct{}
}

// New creates a new Discovery instance
func New(nodeID string, port int) *Discovery {
	return &Discovery{
		nodeID: nodeID,
		port:   port,
		peers:  make(map[string]time.Time),
		stop:   make(chan struct{}),
	}
}

// Start begins broadcasting and listening for peers
func (d *Discovery) Start() {
	go d.broadcast()
	go d.listen()
	go d.cleanup()
	log.Printf("[DISCOVERY] Auto-discovery started (UDP port %d)", broadcastPort)
}

// Stop halts discovery
func (d *Discovery) Stop() {
	close(d.stop)
}

// broadcast sends periodic UDP announcements to the LAN
func (d *Discovery) broadcast() {
	// Broadcast to all local broadcast addresses
	payload, _ := json.Marshal(Announcement{
		NodeID: d.nodeID,
		Port:   d.port,
	})

	ticker := time.NewTicker(broadcastInterval)
	defer ticker.Stop()

	// Send immediately on start
	d.sendBroadcast(payload)

	for {
		select {
		case <-ticker.C:
			d.sendBroadcast(payload)
		case <-d.stop:
			return
		}
	}
}

func (d *Discovery) sendBroadcast(payload []byte) {
	// Get all broadcast addresses for local interfaces
	addrs := getBroadcastAddresses()
	for _, addr := range addrs {
		target := fmt.Sprintf("%s:%d", addr, broadcastPort)
		conn, err := net.Dial("udp4", target)
		if err != nil {
			continue
		}
		conn.Write(payload)
		conn.Close()
	}

	// Also broadcast to 255.255.255.255
	conn, err := net.Dial("udp4", fmt.Sprintf("255.255.255.255:%d", broadcastPort))
	if err == nil {
		conn.Write(payload)
		conn.Close()
	}
}

// listen listens for UDP announcements from peers
func (d *Discovery) listen() {
	addr := &net.UDPAddr{
		Port: broadcastPort,
		IP:   net.IPv4zero,
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		log.Printf("[DISCOVERY] Failed to listen on UDP port %d: %v", broadcastPort, err)
		return
	}
	defer conn.Close()

	buf := make([]byte, maxPacketSize)
	for {
		select {
		case <-d.stop:
			return
		default:
		}

		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			continue
		}

		var ann Announcement
		if err := json.Unmarshal(buf[:n], &ann); err != nil {
			continue
		}

		// Ignore our own announcements
		if ann.NodeID == d.nodeID {
			continue
		}

		// Build the peer's mesh WebSocket URL
		peerIP := remoteAddr.IP.String()
		peerAddr := fmt.Sprintf("ws://%s:%d/ws/mesh", peerIP, ann.Port)

		d.mu.Lock()
		_, known := d.peers[ann.NodeID]
		d.peers[ann.NodeID] = time.Now()
		d.mu.Unlock()

		// If this is a new peer, notify
		if !known {
			log.Printf("[DISCOVERY] Found new peer: %s at %s", ann.NodeID, peerAddr)
			if d.OnPeerFound != nil {
				go d.OnPeerFound(ann.NodeID, peerAddr)
			}
		}
	}
}

// cleanup removes stale peers
func (d *Discovery) cleanup() {
	ticker := time.NewTicker(peerTimeout)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			d.mu.Lock()
			for id, lastSeen := range d.peers {
				if time.Since(lastSeen) > peerTimeout {
					delete(d.peers, id)
					log.Printf("[DISCOVERY] Peer timed out: %s", id)
				}
			}
			d.mu.Unlock()
		case <-d.stop:
			return
		}
	}
}

// getBroadcastAddresses returns broadcast addresses for all local IPv4 interfaces
func getBroadcastAddresses() []string {
	var addrs []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return addrs
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		ifAddrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range ifAddrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil {
				continue
			}

			// Calculate broadcast address: IP | ~mask
			ip := ipnet.IP.To4()
			mask := ipnet.Mask
			broadcast := make(net.IP, 4)
			for i := 0; i < 4; i++ {
				broadcast[i] = ip[i] | ^mask[i]
			}
			addrs = append(addrs, broadcast.String())
		}
	}
	return addrs
}
