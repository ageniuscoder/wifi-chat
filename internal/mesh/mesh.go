package mesh

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"wifi-chat/internal/models"
)

// Mesh manages peer-to-peer connections for multi-hop WiFi relay.
// Each node can connect to upstream peers and accept downstream peers.
// Messages propagate via gossip with deduplication by message ID.
// Supports multi-hop TTL relay for interop with Android MeshRouter.
type Mesh struct {
	NodeID string

	peers   map[string]*Peer
	peerMu  sync.RWMutex

	connectedURLs   map[string]bool // tracks URLs we're already connected/connecting to
	connectedURLsMu sync.Mutex

	seen   map[string]time.Time // msgID -> time seen
	seenMu sync.Mutex

	// Remote users from peers (peerID -> []username)
	remoteUsers   map[string][]string
	remoteUsersMu sync.RWMutex

	// Local users on this node (set by hub)
	localUsers   []string
	localUsersMu sync.RWMutex

	// Store-and-forward outbox (targetUser -> []json)
	outbox   map[string][]string
	outboxMu sync.Mutex

	// Callbacks set by the hub
	OnMessageFromPeer func(msg models.Message)
	OnUsersFromPeer   func(peerID string, users []string)

	// Stop channel for announce goroutine
	stopCh chan struct{}
}

// Peer represents a single mesh peer connection
type Peer struct {
	ID   string
	Conn *websocket.Conn
	Send chan []byte
	mesh *Mesh
}

const (
	MaxTTL            = 7
	AnnounceInterval  = 10 * time.Second
	MaxOutboxPerUser  = 100
)

// New creates a new Mesh instance
func New(nodeID string) *Mesh {
	m := &Mesh{
		NodeID:        nodeID,
		peers:         make(map[string]*Peer),
		connectedURLs: make(map[string]bool),
		seen:          make(map[string]time.Time),
		remoteUsers:   make(map[string][]string),
		outbox:        make(map[string][]string),
		stopCh:        make(chan struct{}),
	}
	go m.announceLoop()
	return m
}

// Stop shuts down background goroutines
func (m *Mesh) Stop() {
	close(m.stopCh)
}

// SetLocalUsers updates the local user list (called by hub on join/leave)
func (m *Mesh) SetLocalUsers(users []string) {
	m.localUsersMu.Lock()
	m.localUsers = users
	m.localUsersMu.Unlock()
}

// GetAllKnownUsers returns local + remote users
func (m *Mesh) GetAllKnownUsers() []string {
	seen := make(map[string]bool)
	var result []string

	m.localUsersMu.RLock()
	for _, u := range m.localUsers {
		if !seen[u] { result = append(result, u); seen[u] = true }
	}
	m.localUsersMu.RUnlock()

	m.remoteUsersMu.RLock()
	for _, users := range m.remoteUsers {
		for _, u := range users {
			if !seen[u] { result = append(result, u); seen[u] = true }
		}
	}
	m.remoteUsersMu.RUnlock()
	return result
}

// IsConnectedTo checks if we're already connected or connecting to a URL
func (m *Mesh) IsConnectedTo(url string) bool {
	m.connectedURLsMu.Lock()
	defer m.connectedURLsMu.Unlock()
	return m.connectedURLs[url]
}

// ConnectPeer dials an upstream peer and maintains the connection with auto-reconnect
func (m *Mesh) ConnectPeer(url string) {
	m.connectedURLsMu.Lock()
	if m.connectedURLs[url] {
		m.connectedURLsMu.Unlock()
		return // already connected or connecting
	}
	m.connectedURLs[url] = true
	m.connectedURLsMu.Unlock()

	go func() {
		for {
			if err := m.dialPeer(url); err != nil {
				log.Printf("[MESH] Peer connect failed (%s): %v — retrying in 5s", url, err)
			} else {
				log.Printf("[MESH] Peer disconnected (%s) — reconnecting in 5s", url)
			}
			select {
			case <-m.stopCh:
				log.Printf("[MESH] Stopping reconnect loop for %s", url)
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
}

func (m *Mesh) dialPeer(url string) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		return err
	}

	// Send hello
	hello := models.Message{
		Type: models.MsgTypeMeshSync,
		From: m.NodeID,
		ID:   models.GenerateMsgID(),
	}
	data, _ := json.Marshal(hello)
	conn.WriteMessage(websocket.TextMessage, data)

	peer := &Peer{
		ID:   url,
		Conn: conn,
		Send: make(chan []byte, 256),
		mesh: m,
	}

	m.peerMu.Lock()
	m.peers[url] = peer
	m.peerMu.Unlock()

	log.Printf("[MESH] Connected to peer: %s", url)

	done := make(chan struct{})
	go peer.writePump(done)
	peer.readPump() // blocks
	close(done)

	m.peerMu.Lock()
	delete(m.peers, url)
	m.peerMu.Unlock()

	return nil
}

// AcceptPeer registers an incoming peer connection (called by server on /ws/mesh)
func (m *Mesh) AcceptPeer(conn *websocket.Conn, peerID string) {
	peer := &Peer{
		ID:   peerID,
		Conn: conn,
		Send: make(chan []byte, 256),
		mesh: m,
	}

	m.peerMu.Lock()
	m.peers[peerID] = peer
	m.peerMu.Unlock()

	log.Printf("[MESH] Accepted peer: %s", peerID)

	done := make(chan struct{})
	go peer.writePump(done)
	peer.readPump() // blocks
	close(done)

	m.peerMu.Lock()
	delete(m.peers, peerID)
	m.peerMu.Unlock()

	log.Printf("[MESH] Peer disconnected: %s", peerID)
}

// Propagate sends a message to all peers (called when local hub broadcasts).
// Sets multi-hop fields for Android MeshRouter interop.
func (m *Mesh) Propagate(msg models.Message) {
	if msg.ID == "" {
		msg.ID = models.GenerateMsgID()
	}
	// Set distributed mesh fields if not already set
	if msg.MessageID == "" {
		msg.MessageID = msg.ID
	}
	if msg.TTL == 0 {
		msg.TTL = MaxTTL
	}
	if msg.OriginNode == "" {
		msg.OriginNode = m.NodeID
	}
	if len(msg.HopPath) == 0 {
		msg.HopPath = []string{m.NodeID}
	}

	if m.alreadySeen(msg.ID) {
		return
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	m.peerMu.RLock()
	defer m.peerMu.RUnlock()

	for _, peer := range m.peers {
		select {
		case peer.Send <- data:
		default:
		}
	}
}

// propagateExcept sends to all peers except one (used to avoid echo)
func (m *Mesh) propagateExcept(msg models.Message, exceptID string) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	m.peerMu.RLock()
	defer m.peerMu.RUnlock()

	for id, peer := range m.peers {
		if id == exceptID {
			continue
		}
		select {
		case peer.Send <- data:
		default:
		}
	}
}

// SyncUsers broadcasts local user list to all peers
func (m *Mesh) SyncUsers(users []string) {
	msg := models.Message{
		Type:  models.MsgTypeMeshSync,
		ID:    models.GenerateMsgID(),
		Users: users,
		From:  m.NodeID,
	}
	data, _ := json.Marshal(msg)

	m.peerMu.RLock()
	defer m.peerMu.RUnlock()
	for _, peer := range m.peers {
		select {
		case peer.Send <- data:
		default:
		}
	}
}

// HasPeers returns true if mesh has any active peers
func (m *Mesh) HasPeers() bool {
	m.peerMu.RLock()
	defer m.peerMu.RUnlock()
	return len(m.peers) > 0
}

// --- announce loop (sends topology advertisement to all peers) ---

func (m *Mesh) announceLoop() {
	ticker := time.NewTicker(AnnounceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.broadcastAnnounce()
		case <-m.stopCh:
			return
		}
	}
}

func (m *Mesh) broadcastAnnounce() {
	users := m.GetAllKnownUsers()

	m.peerMu.RLock()
	neighbors := make([]string, 0, len(m.peers))
	for id := range m.peers {
		neighbors = append(neighbors, id)
	}
	m.peerMu.RUnlock()

	announceID := models.GenerateMsgID()
	msg := models.Message{
		Type:       models.MsgTypeAnnounce,
		ID:         announceID,
		MessageID:  announceID,
		NodeID:     m.NodeID,
		Users:      users,
		Neighbors:  neighbors,
		Timestamp:  models.NewTimestamp(),
		TTL:        MaxTTL,
		OriginNode: m.NodeID,
		HopPath:    []string{m.NodeID},
	}
	m.alreadySeen(announceID) // mark own announce as seen
	data, _ := json.Marshal(msg)

	m.peerMu.RLock()
	defer m.peerMu.RUnlock()
	for _, peer := range m.peers {
		select {
		case peer.Send <- data:
		default:
		}
	}
}

func (m *Mesh) handleAnnounce(peerID string, msg models.Message) {
	m.handleRemoteUsers(peerID, msg.Users)

	// Notify hub so it can update its user list and broadcast to local clients
	if m.OnUsersFromPeer != nil && msg.Users != nil {
		m.OnUsersFromPeer(peerID, msg.Users)
	}

	// Flush outbox for newly reachable users
	if msg.Users != nil {
		for _, user := range msg.Users {
			go m.FlushOutboxForUser(user)
		}
	}

	log.Printf("[MESH] Announce from %s (node=%s): users=%v neighbors=%v",
		peerID, msg.NodeID, msg.Users, msg.Neighbors)
}

func (m *Mesh) handleRemoteUsers(peerID string, users []string) {
	if users == nil {
		return
	}
	m.remoteUsersMu.Lock()
	m.remoteUsers[peerID] = users
	m.remoteUsersMu.Unlock()
}

// --- store-and-forward outbox ---

// EnqueueForUser queues a serialized message for an unreachable user
func (m *Mesh) EnqueueForUser(user string, msgJSON string) {
	m.outboxMu.Lock()
	defer m.outboxMu.Unlock()
	q := m.outbox[user]
	if len(q) >= MaxOutboxPerUser {
		q = q[1:] // drop oldest
	}
	m.outbox[user] = append(q, msgJSON)
	log.Printf("[MESH] Queued message for offline user: %s (queue: %d)", user, len(m.outbox[user]))
}

// FlushOutboxForUser sends all queued messages for a user and clears the queue
func (m *Mesh) FlushOutboxForUser(user string) {
	m.outboxMu.Lock()
	queued := m.outbox[user]
	delete(m.outbox, user)
	m.outboxMu.Unlock()

	if len(queued) == 0 {
		return
	}

	log.Printf("[MESH] Flushing %d queued messages for %s", len(queued), user)
	m.peerMu.RLock()
	defer m.peerMu.RUnlock()
	for _, raw := range queued {
		data := []byte(raw)
		for _, peer := range m.peers {
			select {
			case peer.Send <- data:
			default:
			}
		}
	}
}

// IsRemoteUser checks if a user is reachable through any peer
func (m *Mesh) IsRemoteUser(username string) bool {
	m.remoteUsersMu.RLock()
	defer m.remoteUsersMu.RUnlock()
	for _, users := range m.remoteUsers {
		for _, u := range users {
			if u == username {
				return true
			}
		}
	}
	return false
}

// --- dedup ---

func (m *Mesh) alreadySeen(id string) bool {
	m.seenMu.Lock()
	defer m.seenMu.Unlock()

	now := time.Now()
	if _, ok := m.seen[id]; ok {
		return true
	}
	m.seen[id] = now

	// Time-based cleanup: remove entries older than 5 minutes
	if len(m.seen) > 10000 {
		cutoff := now.Add(-5 * time.Minute)
		for k, t := range m.seen {
			if t.Before(cutoff) {
				delete(m.seen, k)
			}
		}
	}
	return false
}

// --- Peer pumps ---

func (p *Peer) readPump() {
	defer p.Conn.Close()

	p.Conn.SetReadLimit(65536)
	p.Conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	p.Conn.SetPongHandler(func(string) error {
		p.Conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})

	for {
		_, data, err := p.Conn.ReadMessage()
		if err != nil {
			return
		}

		var msg models.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		// Mesh sync = user presence update (legacy + Go interop)
		if msg.Type == models.MsgTypeMeshSync {
			if p.mesh.OnUsersFromPeer != nil && msg.Users != nil {
				p.mesh.OnUsersFromPeer(p.ID, msg.Users)
			}
			p.mesh.handleRemoteUsers(p.ID, msg.Users)
			continue
		}

		// Announce = Android MeshRouter topology advertisement
		if msg.Type == models.MsgTypeAnnounce {
			dedupID := msg.MessageID
			if dedupID == "" {
				dedupID = msg.ID
			}
			if dedupID != "" && p.mesh.alreadySeen(dedupID) {
				continue
			}
			p.mesh.handleAnnounce(p.ID, msg)

			// Forward announce to other peers for multi-hop discovery
			if msg.TTL > 0 {
				msg.TTL--
				msg.Hops++
				msg.HopPath = append(msg.HopPath, p.mesh.NodeID)
				p.mesh.propagateExcept(msg, p.ID)
			}
			continue
		}

		// Dedup: prefer message_id (Android), fall back to id (Go)
		dedupID := msg.MessageID
		if dedupID == "" {
			dedupID = msg.ID
		}
		if dedupID == "" {
			dedupID = models.GenerateMsgID()
			msg.ID = dedupID
			msg.MessageID = dedupID
		}
		if p.mesh.alreadySeen(dedupID) {
			continue
		}

		// Check TTL (multi-hop support)
		if msg.TTL > 0 {
			msg.TTL--
			msg.Hops++
			msg.HopPath = append(msg.HopPath, p.mesh.NodeID)
		}

		// Deliver locally
		if p.mesh.OnMessageFromPeer != nil {
			p.mesh.OnMessageFromPeer(msg)
		}

		// Forward to other peers if TTL allows (split-horizon: exclude ingress)
		if msg.TTL > 0 {
			p.mesh.propagateExcept(msg, p.ID)
		}
	}
}

func (p *Peer) writePump(done chan struct{}) {
	ticker := time.NewTicker(45 * time.Second)
	defer func() {
		ticker.Stop()
		p.Conn.Close()
	}()

	for {
		select {
		case data, ok := <-p.Send:
			if !ok {
				return
			}
			p.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := p.Conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		case <-ticker.C:
			p.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := p.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}
