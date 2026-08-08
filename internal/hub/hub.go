package hub

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"wifi-chat/internal/mesh"
	"wifi-chat/internal/models"
	"wifi-chat/internal/store"
)

// Hub maintains the set of active clients and broadcasts messages
type Hub struct {
	// Registered clients
	Clients map[*Client]bool

	// Username -> Client lookup
	UserClients map[string]*Client

	// Room -> set of clients
	Rooms map[string]map[*Client]bool

	// Register requests from clients
	Register chan *Client

	// Unregister requests from clients
	Unregister chan *Client

	// Message store
	Store *store.MessageStore

	// Mesh for multi-hop relay
	Mesh *mesh.Mesh

	// Remote users from mesh peers (peerID -> []username)
	RemoteUsers map[string][]string

	// Join cooldown to prevent duplicate "joined" broadcasts on rapid reconnects
	joinCooldown map[string]time.Time

	// Rate limiting: username -> last message time
	rateLimiter map[string]time.Time

	// Max msg limit
	MaxContentLen int64

	mu sync.RWMutex
}

// NewHub creates a new Hub
func NewHub(s *store.MessageStore, maxContentLen int64) *Hub {
	h := &Hub{
		Clients:       make(map[*Client]bool),
		UserClients:   make(map[string]*Client),
		Rooms:         make(map[string]map[*Client]bool),
		Register:      make(chan *Client),
		Unregister:    make(chan *Client),
		joinCooldown:  make(map[string]time.Time),
		rateLimiter:   make(map[string]time.Time),
		Store:         s,
		RemoteUsers:   make(map[string][]string),
		MaxContentLen: maxContentLen,
	}
	// Create default room
	h.Rooms["general"] = make(map[*Client]bool)
	return h
}

// SetMesh sets the mesh and wires up callbacks
func (h *Hub) SetMesh(m *mesh.Mesh) {
	h.Mesh = m
	m.OnMessageFromPeer = h.InjectFromMesh
	m.OnUsersFromPeer = h.onRemoteUsers
}

// Run starts the hub's main event loop
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.Register:
			h.mu.Lock()
			h.Clients[client] = true
			h.mu.Unlock()

		case client := <-h.Unregister:
			h.mu.Lock()
			if _, ok := h.Clients[client]; ok {
				delete(h.Clients, client)

				// Check if this client is still the current owner of the username.
				// If a newer client has replaced it (reconnect/refresh), don't
				// broadcast "user left" or remove from UserClients.
				isCurrentOwner := false
				if client.Username != "" {
					if current, exists := h.UserClients[client.Username]; exists && current == client {
						isCurrentOwner = true
						delete(h.UserClients, client.Username)
					}
				}

				// Remove from all rooms
				for room, members := range h.Rooms {
					if _, ok := members[client]; ok {
						delete(members, client)
						// Only broadcast "user left" if this was the actual owner
						if isCurrentOwner && client.Username != "" {
							h.broadcastToRoomLocked(room, models.Message{
								Type:      models.MsgTypeUserLeft,
								Username:  client.Username,
								Room:      room,
								Timestamp: models.NewTimestamp(),
							}, nil)
							h.sendRoomUsersLocked(room)
						}
					}
				}
				client.CloseSend()

				if isCurrentOwner {
					// Broadcast updated user list
					h.broadcastUserListLocked()

					// Sync local users to mesh
					if h.Mesh != nil {
						usernames := h.getLocalUsernamesLocked()
						h.Mesh.SetLocalUsers(usernames)
						go h.Mesh.SyncUsers(usernames)
					}
				}
			}
			h.mu.Unlock()
		}
	}
}

// ProcessMessage handles an incoming message from a client
func (h *Hub) ProcessMessage(client *Client, rawMsg []byte) {
	// Rate limiting: max 30 messages per second per client
	if client.Username != "" {
		h.mu.Lock()
		now := time.Now()
		if last, ok := h.rateLimiter[client.Username]; ok && now.Sub(last) < 33*time.Millisecond {
			h.mu.Unlock()
			return // throttle
		}
		h.rateLimiter[client.Username] = now
		h.mu.Unlock()
	}

	var msg models.Message
	if err := json.Unmarshal(rawMsg, &msg); err != nil {
		client.SendJSON(models.Message{
			Type:  models.MsgTypeError,
			Error: "Invalid message format",
		})
		return
	}

	switch msg.Type {
	case models.MsgTypeJoin:
		h.handleJoin(client, msg)
	case models.MsgTypeMessage:
		h.handleMessage(client, msg)
	case models.MsgTypeImage:
		h.handleImage(client, msg)
	case models.MsgTypeDM:
		h.handleDM(client, msg)
	case models.MsgTypeCreateRoom:
		h.handleCreateRoom(client, msg)
	case models.MsgTypeJoinRoom:
		h.handleJoinRoom(client, msg)
	case models.MsgTypeLeaveRoom:
		h.handleLeaveRoom(client, msg)
	case models.MsgTypeTyping:
		h.handleTyping(client, msg)
	case models.MsgTypeListRooms:
		h.handleListRooms(client)
	case models.MsgTypeListUsers:
		h.handleListUsers(client)
	case models.MsgTypeGetDMHistory:
		h.handleGetDMHistory(client, msg)
	case models.MsgTypeKeyExchange:
		h.handleKeyExchange(client, msg)
	case models.MsgTypeCallRequest, models.MsgTypeCallAccept, models.MsgTypeCallReject,
		models.MsgTypeCallOffer, models.MsgTypeCallAnswer, models.MsgTypeCallICE,
		models.MsgTypeCallEnd:
		h.handleCallSignal(client, msg)
	case models.MsgTypeDeliveryAck, models.MsgTypeReadReceipt:
		h.handleDeliveryReceipt(client, msg)
	case models.MsgTypeAnnounce, models.MsgTypePeerDiscovery, models.MsgTypeStoreForward:
		// These are mesh-level messages — relay through mesh if present
		if h.Mesh != nil {
			h.Mesh.Propagate(msg)
		}
	default:
		client.SendJSON(models.Message{
			Type:  models.MsgTypeError,
			Error: "Unknown message type: " + string(msg.Type),
		})
	}
}

func (h *Hub) handleJoin(client *Client, msg models.Message) {
	username := msg.Username
	if username == "" {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "Username is required"})
		return
	}
	if !models.ValidateUsername(username) {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "Invalid username (1-32 alphanumeric characters, _ - . allowed)"})
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Check cooldown to prevent duplicate "joined" broadcasts on rapid reconnects
	// (handles race condition where old Unregister removes username before new handleJoin checks)
	now := time.Now()
	isReconnect := false
	if lastJoin, exists := h.joinCooldown[username]; exists && now.Sub(lastJoin) < 5*time.Second {
		isReconnect = true
		// Remove expired cooldown entries
		for u, t := range h.joinCooldown {
			if now.Sub(t) > 30*time.Second {
				delete(h.joinCooldown, u)
			}
		}
	} else {
		h.joinCooldown[username] = now
	}

	// If username is taken, kick the old connection (likely a stale refresh)
	if !isReconnect {
		if oldClient, exists := h.UserClients[username]; exists {
			isReconnect = true
			// Remove old client from all rooms
			for room, members := range h.Rooms {
				delete(members, oldClient)
				_ = room
			}
			delete(h.Clients, oldClient)
			delete(h.UserClients, username)
			oldClient.CloseSend()
		}
	}

	client.Username = username
	client.PublicKey = msg.PublicKey
	h.UserClients[username] = client

	// Auto-join general room
	if h.Rooms["general"] == nil {
		h.Rooms["general"] = make(map[*Client]bool)
	}
	h.Rooms["general"][client] = true
	client.JoinRoom("general")

	// Send room list to the new user
	rooms := h.getRoomListLocked()
	client.SendJSON(models.Message{
		Type:  models.MsgTypeRoomList,
		Rooms: rooms,
	})

	// Send user list with public keys for E2EE
	users := h.getUserListLocked()
	client.SendJSON(models.Message{
		Type:       models.MsgTypeUserList,
		Users:      users,
		PublicKeys: h.getPublicKeysLocked(),
	})

	// Send message history for general room
	history := h.Store.GetRoomMessages("general", 50)
	if len(history) > 0 {
		histMsgs := make([]models.Message, len(history))
		for i, m := range history {
			histMsgs[i] = models.Message{
				Type:      models.MsgTypeMessage,
				From:      m.From,
				Room:      m.Room,
				Content:   m.Content,
				ImageURL:  m.ImageURL,
				Timestamp: m.Timestamp.UnixMilli(),
				MessageID: m.MsgID,
			}
		}
		client.SendJSON(models.Message{
			Type:     models.MsgTypeHistory,
			Room:     "general",
			Messages: histMsgs,
		})
	}

	// Only broadcast user joined for new logins, not reconnects
	if !isReconnect {
		h.broadcastToRoomLocked("general", models.Message{
			Type:      models.MsgTypeUserJoined,
			Username:  username,
			Room:      "general",
			PublicKey: msg.PublicKey,
			Timestamp: models.NewTimestamp(),
		}, client)
		log.Printf("User %s joined", username)
	}

	// Send system welcome
	client.SendJSON(models.Message{
		Type:      models.MsgTypeSystem,
		Content:   "Welcome to WiFi Chat, " + username + "! You've joined #general.",
		Timestamp: models.NewTimestamp(),
	})

	h.broadcastUserListLocked()
	h.sendRoomUsersLocked("general")

	// Sync local users to mesh
	if h.Mesh != nil {
		usernames := h.getLocalUsernamesLocked()
		h.Mesh.SetLocalUsers(usernames)
		go h.Mesh.SyncUsers(usernames)
	}

}

func (h *Hub) handleMessage(client *Client, msg models.Message) {
	if client.Username == "" {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "You must join first"})
		return
	}
	room := msg.Room
	if room == "" {
		room = "general"
	}

	h.mu.RLock()
	_, roomExists := h.Rooms[room]
	h.mu.RUnlock()

	if !roomExists {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "Room does not exist: " + room})
		return
	}

	if !client.IsInRoom(room) {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "You are not in room: " + room})
		return
	}

	msg.Content = models.TruncateContent(msg.Content, int(h.MaxContentLen))

	ts := models.NewTimestamp()
	msgID := msg.MessageID
	if msgID == "" {
		msgID = models.GenerateMsgID()
	}

	// Store message
	h.Store.SaveMessage(store.StoredMessage{
		Room:     room,
		From:     client.Username,
		Content:  msg.Content,
		ImageURL: msg.ImageURL,
		MsgID:    msgID,
	})

	// Broadcast to room
	outMsg := models.Message{
		Type:      models.MsgTypeMessage,
		From:      client.Username,
		Room:      room,
		Content:   msg.Content,
		ImageURL:  msg.ImageURL,
		Timestamp: ts,
		MessageID: msgID,
	}

	h.mu.RLock()
	h.broadcastToRoomRLocked(room, outMsg, nil)
	h.mu.RUnlock()

	// Propagate to mesh peers
	if h.Mesh != nil {
		h.Mesh.Propagate(outMsg)
	}
}

func (h *Hub) handleImage(client *Client, msg models.Message) {
	if client.Username == "" {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "You must join first"})
		return
	}
	if msg.ImageURL == "" {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "Image URL is required"})
		return
	}

	// Route as DM or room message based on presence of "to" field
	if msg.To != "" {
		msg.Type = models.MsgTypeDM
		h.handleDM(client, msg)
	} else {
		msg.Type = models.MsgTypeMessage
		h.handleMessage(client, msg)
	}
}

func (h *Hub) handleDM(client *Client, msg models.Message) {
	if client.Username == "" {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "You must join first"})
		return
	}
	if msg.To == "" {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "Recipient is required"})
		return
	}
	if msg.To == client.Username {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "Cannot DM yourself"})
		return
	}

	h.mu.RLock()
	target, exists := h.UserClients[msg.To]
	h.mu.RUnlock()

	msg.Content = models.TruncateContent(msg.Content, int(h.MaxContentLen))

	if !exists {
		// Target might be on a remote mesh node — propagate and echo to sender
		if h.Mesh != nil && h.isRemoteUser(msg.To) {
			ts := models.NewTimestamp()
			remoteMsgID := msg.MessageID
			if remoteMsgID == "" {
				remoteMsgID = models.GenerateMsgID()
			}

			// Store remote DM so it appears in get_dm_history
			h.Store.SaveDM(store.StoredMessage{
				From:      client.Username,
				To:        msg.To,
				Content:   msg.Content,
				ImageURL:  msg.ImageURL,
				Encrypted: msg.Encrypted,
				MsgID:     remoteMsgID,
			})

			outMsg := models.Message{
				Type:      models.MsgTypeDM,
				From:      client.Username,
				To:        msg.To,
				Content:   msg.Content,
				ImageURL:  msg.ImageURL,
				Timestamp: ts,
				Encrypted: msg.Encrypted,
				MessageID: remoteMsgID,
			}
			client.SendJSON(outMsg) // echo to sender
			h.Mesh.Propagate(outMsg)
			return
		}
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "User not found: " + msg.To})
		return
	}

	ts := models.NewTimestamp()

	dmMsgID := msg.MessageID
	if dmMsgID == "" {
		dmMsgID = models.GenerateMsgID()
	}

	// Store DM
	h.Store.SaveDM(store.StoredMessage{
		From:      client.Username,
		To:        msg.To,
		Content:   msg.Content,
		ImageURL:  msg.ImageURL,
		Encrypted: msg.Encrypted,
		MsgID:     dmMsgID,
	})

	outMsg := models.Message{
		Type:      models.MsgTypeDM,
		From:      client.Username,
		To:        msg.To,
		Content:   msg.Content,
		ImageURL:  msg.ImageURL,
		Timestamp: ts,
		Encrypted: msg.Encrypted,
		MessageID: dmMsgID,
	}

	// Send to recipient
	target.SendJSON(outMsg)
	// Echo back to sender
	client.SendJSON(outMsg)

	// Auto-send delivery_ack for locally-delivered DMs so sender can update status
	if dmMsgID != "" {
		h.Store.UpdateDMStatus(dmMsgID, "delivered")
		client.SendJSON(models.Message{
			Type:      models.MsgTypeDeliveryAck,
			From:      msg.To,
			To:        client.Username,
			RefID:     dmMsgID,
			Status:    "delivered",
			Timestamp: ts,
		})
	}

	// Propagate DM to mesh (in case recipient is remote)
	if h.Mesh != nil {
		h.Mesh.Propagate(outMsg)
	}
}

func (h *Hub) handleGetDMHistory(client *Client, msg models.Message) {
	if client.Username == "" {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "You must join first"})
		return
	}
	peer := msg.To
	if peer == "" {
		return
	}

	history := h.Store.GetDMs(client.Username, peer, 50)
	if len(history) > 0 {
		histMsgs := make([]models.Message, len(history))
		for i, m := range history {
			histMsgs[i] = models.Message{
				Type:      models.MsgTypeDM,
				From:      m.From,
				To:        m.To,
				Content:   m.Content,
				ImageURL:  m.ImageURL,
				Timestamp: m.Timestamp.UnixMilli(),
				Encrypted: m.Encrypted,
				MessageID: m.MsgID,
				Status:    m.Status,
			}
		}
		client.SendJSON(models.Message{
			Type:     models.MsgTypeDMHistory,
			From:     peer,
			Messages: histMsgs,
		})
	}
}

func (h *Hub) handleCreateRoom(client *Client, msg models.Message) {
	if client.Username == "" {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "You must join first"})
		return
	}
	if msg.Room == "" {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "Room name is required"})
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.Rooms[msg.Room]; exists {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "Room already exists: " + msg.Room})
		return
	}

	h.Rooms[msg.Room] = make(map[*Client]bool)
	h.Rooms[msg.Room][client] = true
	client.JoinRoom(msg.Room)

	log.Printf("Room %s created by %s", msg.Room, client.Username)

	// Broadcast updated room list to all clients
	rooms := h.getRoomListLocked()
	for c := range h.Clients {
		if c.Username != "" {
			c.SendJSON(models.Message{
				Type:  models.MsgTypeRoomList,
				Rooms: rooms,
			})
		}
	}

	client.SendJSON(models.Message{
		Type:      models.MsgTypeSystem,
		Content:   "Room #" + msg.Room + " created. You've been added.",
		Room:      msg.Room,
		Timestamp: models.NewTimestamp(),
	})

	h.sendRoomUsersLocked(msg.Room)
}

func (h *Hub) handleJoinRoom(client *Client, msg models.Message) {
	if client.Username == "" {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "You must join first"})
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	members, exists := h.Rooms[msg.Room]
	if !exists {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "Room does not exist: " + msg.Room})
		return
	}

	if _, already := members[client]; already {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "Already in room: " + msg.Room})
		return
	}

	members[client] = true
	client.JoinRoom(msg.Room)

	// Send history
	history := h.Store.GetRoomMessages(msg.Room, 50)
	if len(history) > 0 {
		histMsgs := make([]models.Message, len(history))
		for i, m := range history {
			histMsgs[i] = models.Message{
				Type:      models.MsgTypeMessage,
				From:      m.From,
				Room:      m.Room,
				Content:   m.Content,
				ImageURL:  m.ImageURL,
				Timestamp: m.Timestamp.UnixMilli(),
				MessageID: m.MsgID,
			}
		}
		client.SendJSON(models.Message{
			Type:     models.MsgTypeHistory,
			Room:     msg.Room,
			Messages: histMsgs,
		})
	}

	h.broadcastToRoomLocked(msg.Room, models.Message{
		Type:      models.MsgTypeUserJoined,
		Username:  client.Username,
		Room:      msg.Room,
		Timestamp: models.NewTimestamp(),
	}, client)

	client.SendJSON(models.Message{
		Type:      models.MsgTypeSystem,
		Content:   "You joined #" + msg.Room,
		Room:      msg.Room,
		Timestamp: models.NewTimestamp(),
	})

	h.sendRoomUsersLocked(msg.Room)
}

func (h *Hub) handleLeaveRoom(client *Client, msg models.Message) {
	if client.Username == "" {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "You must join first"})
		return
	}
	if msg.Room == "general" {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "Cannot leave #general"})
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	members, exists := h.Rooms[msg.Room]
	if !exists {
		return
	}

	delete(members, client)
	client.LeaveRoom(msg.Room)

	h.broadcastToRoomLocked(msg.Room, models.Message{
		Type:      models.MsgTypeUserLeft,
		Username:  client.Username,
		Room:      msg.Room,
		Timestamp: models.NewTimestamp(),
	}, nil)

	h.sendRoomUsersLocked(msg.Room)
}

func (h *Hub) handleTyping(client *Client, msg models.Message) {
	if client.Username == "" {
		return
	}

	// DM typing: forward to specific recipient
	if msg.To != "" {
		h.mu.RLock()
		if target, ok := h.UserClients[msg.To]; ok {
			target.SendJSON(models.Message{
				Type:      models.MsgTypeTyping,
				Username:  client.Username,
				To:        msg.To,
				Timestamp: models.NewTimestamp(),
			})
		}
		h.mu.RUnlock()
		// Propagate DM typing to mesh
		if h.Mesh != nil {
			h.Mesh.Propagate(models.Message{
				Type:     models.MsgTypeTyping,
				Username: client.Username,
				To:       msg.To,
			})
		}
		return
	}

	// Room typing
	room := msg.Room
	if room == "" {
		room = "general"
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	h.broadcastToRoomRLocked(room, models.Message{
		Type:      models.MsgTypeTyping,
		Username:  client.Username,
		Room:      room,
		Timestamp: models.NewTimestamp(),
	}, client)
}

func (h *Hub) handleListRooms(client *Client) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	rooms := h.getRoomListLocked()
	client.SendJSON(models.Message{
		Type:  models.MsgTypeRoomList,
		Rooms: rooms,
	})
}

func (h *Hub) handleListUsers(client *Client) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	users := h.getUserListLocked()
	client.SendJSON(models.Message{
		Type:       models.MsgTypeUserList,
		Users:      users,
		PublicKeys: h.getPublicKeysLocked(),
	})
}

func (h *Hub) handleCallSignal(client *Client, msg models.Message) {
	if client.Username == "" {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "You must join first"})
		return
	}
	if msg.To == "" {
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "Call target is required"})
		return
	}

	h.mu.RLock()
	target, exists := h.UserClients[msg.To]
	h.mu.RUnlock()

	if !exists {
		// Target might be on a remote mesh node — propagate
		if h.Mesh != nil {
			msg.From = client.Username
			h.Mesh.Propagate(msg)
			log.Printf("Call signal %s: %s -> %s (via mesh)", msg.Type, client.Username, msg.To)
			return
		}
		client.SendJSON(models.Message{Type: models.MsgTypeError, Error: "User not online: " + msg.To})
		return
	}

	// Forward the signaling message to the target, stamping From
	msg.From = client.Username
	target.SendJSON(msg)

	log.Printf("Call signal %s: %s -> %s", msg.Type, client.Username, msg.To)

	// Also propagate call signals to mesh
	if h.Mesh != nil {
		h.Mesh.Propagate(msg)
	}
}

// --- helpers (must be called with lock held) ---

func (h *Hub) getRoomListLocked() []string {
	rooms := make([]string, 0, len(h.Rooms))
	for r := range h.Rooms {
		rooms = append(rooms, r)
	}
	return rooms
}

func (h *Hub) getUserListLocked() []string {
	users := make([]string, 0, len(h.UserClients))
	for u := range h.UserClients {
		users = append(users, u)
	}
	// Include remote users from mesh peers
	seen := make(map[string]bool, len(users))
	for _, u := range users {
		seen[u] = true
	}
	for _, remoteList := range h.RemoteUsers {
		for _, u := range remoteList {
			if !seen[u] {
				users = append(users, u)
				seen[u] = true
			}
		}
	}
	return users
}

// GetLocalUsernames returns only locally connected usernames (for mesh sync)
func (h *Hub) GetLocalUsernames() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.getLocalUsernamesLocked()
}

// getLocalUsernamesLocked returns usernames without acquiring the lock (caller must hold it)
func (h *Hub) getLocalUsernamesLocked() []string {
	users := make([]string, 0, len(h.UserClients))
	for u := range h.UserClients {
		users = append(users, u)
	}
	return users
}

func (h *Hub) getPublicKeysLocked() map[string]string {
	keys := make(map[string]string)
	for username, client := range h.UserClients {
		if client.PublicKey != "" {
			keys[username] = client.PublicKey
		}
	}
	return keys
}

func (h *Hub) handleKeyExchange(client *Client, msg models.Message) {
	if msg.To == "" || client.Username == "" {
		return
	}
	msg.From = client.Username

	h.mu.RLock()
	target, exists := h.UserClients[msg.To]
	h.mu.RUnlock()
	if exists {
		target.SendJSON(msg)
	}
	// Propagate via mesh so remote peers can establish E2EE
	if h.Mesh != nil {
		h.Mesh.Propagate(msg)
	}
}

func (h *Hub) isRemoteUser(username string) bool {
	h.mu.RLock()
	for _, remoteList := range h.RemoteUsers {
		for _, u := range remoteList {
			if u == username {
				h.mu.RUnlock()
				return true
			}
		}
	}
	h.mu.RUnlock()
	// Also check mesh's remote users
	if h.Mesh != nil && h.Mesh.IsRemoteUser(username) {
		return true
	}
	return false
}

// handleDeliveryReceipt forwards delivery_ack and read_receipt to target user
func (h *Hub) handleDeliveryReceipt(client *Client, msg models.Message) {
	if msg.To == "" || client.Username == "" {
		return
	}
	msg.From = client.Username

	// Update stored message status so dm_history reflects delivery/read state
	if msg.RefID != "" {
		status := msg.Status
		if status == "" {
			if msg.Type == models.MsgTypeReadReceipt {
				status = "read"
			} else {
				status = "delivered"
			}
		}
		h.Store.UpdateDMStatus(msg.RefID, status)
	}

	h.mu.RLock()
	target, exists := h.UserClients[msg.To]
	h.mu.RUnlock()

	if exists {
		target.SendJSON(msg)
	}
	// Also propagate via mesh so remote peers get receipts
	if h.Mesh != nil {
		h.Mesh.Propagate(msg)
	}
}

func (h *Hub) onRemoteUsers(peerID string, users []string) {
	h.mu.Lock()
	h.RemoteUsers[peerID] = users
	h.broadcastUserListLocked()
	h.mu.Unlock()
	log.Printf("[MESH] Remote users from %s: %v", peerID, users)
}

// InjectFromMesh processes a message received from a mesh peer
func (h *Hub) InjectFromMesh(msg models.Message) {
	switch msg.Type {
	case models.MsgTypeMessage:
		// Store and broadcast to local clients in that room
		h.Store.SaveMessage(store.StoredMessage{
			Room:     msg.Room,
			From:     msg.From,
			Content:  msg.Content,
			ImageURL: msg.ImageURL,
			MsgID:    msg.MessageID,
		})
		h.mu.RLock()
		h.broadcastToRoomRLocked(msg.Room, msg, nil)
		h.mu.RUnlock()

	case models.MsgTypeDM:
		// Deliver to local recipient if present
		h.mu.RLock()
		if target, ok := h.UserClients[msg.To]; ok {
			target.SendJSON(msg)
			// Send delivery ack back through mesh
			if h.Mesh != nil && msg.MessageID != "" {
				ack := models.Message{
					Type:      models.MsgTypeDeliveryAck,
					From:      msg.To,
					To:        msg.From,
					RefID:     msg.MessageID,
					Status:    "delivered",
					Timestamp: models.NewTimestamp(),
				}
				h.Mesh.Propagate(ack)
			}
		}
		// Also echo to sender if they are local
		if sender, ok := h.UserClients[msg.From]; ok {
			sender.SendJSON(msg)
		}
		h.mu.RUnlock()
		// Store locally
		h.Store.SaveDM(store.StoredMessage{
			From:      msg.From,
			To:        msg.To,
			Content:   msg.Content,
			ImageURL:  msg.ImageURL,
			MsgID:     msg.MessageID,
			Encrypted: msg.Encrypted,
		})

	case models.MsgTypeDeliveryAck, models.MsgTypeReadReceipt:
		// Update stored message status
		if msg.RefID != "" {
			status := msg.Status
			if status == "" {
				if msg.Type == models.MsgTypeReadReceipt {
					status = "read"
				} else {
					status = "delivered"
				}
			}
			h.Store.UpdateDMStatus(msg.RefID, status)
		}
		// Deliver receipt to local target
		h.mu.RLock()
		if target, ok := h.UserClients[msg.To]; ok {
			target.SendJSON(msg)
		}
		h.mu.RUnlock()

	case models.MsgTypeCallRequest, models.MsgTypeCallAccept, models.MsgTypeCallReject,
		models.MsgTypeCallOffer, models.MsgTypeCallAnswer, models.MsgTypeCallICE,
		models.MsgTypeCallEnd:
		// Deliver call signal to local recipient if present
		h.mu.RLock()
		if target, ok := h.UserClients[msg.To]; ok {
			target.SendJSON(msg)
		}
		h.mu.RUnlock()

	case models.MsgTypeKeyExchange:
		// Forward key exchange to local target
		h.mu.RLock()
		if target, ok := h.UserClients[msg.To]; ok {
			target.SendJSON(msg)
		}
		h.mu.RUnlock()

	case models.MsgTypeTyping:
		if msg.To != "" {
			// DM typing from mesh peer — forward to local recipient
			h.mu.RLock()
			if target, ok := h.UserClients[msg.To]; ok {
				target.SendJSON(msg)
			}
			h.mu.RUnlock()
		} else if msg.Room != "" {
			// Broadcast typing indicator from mesh peer to local room members
			h.mu.RLock()
			h.broadcastToRoomRLocked(msg.Room, msg, nil)
			h.mu.RUnlock()
		}

	case models.MsgTypeSystem:
		h.mu.RLock()
		if msg.Room != "" {
			h.broadcastToRoomRLocked(msg.Room, msg, nil)
		}
		h.mu.RUnlock()
	}
}

func (h *Hub) broadcastToRoomLocked(room string, msg models.Message, exclude *Client) {
	members := h.Rooms[room]
	for c := range members {
		if c != exclude && c.Username != "" {
			c.SendJSON(msg)
		}
	}
}

func (h *Hub) broadcastToRoomRLocked(room string, msg models.Message, exclude *Client) {
	members := h.Rooms[room]
	for c := range members {
		if c != exclude && c.Username != "" {
			c.SendJSON(msg)
		}
	}
}

func (h *Hub) broadcastUserListLocked() {
	users := h.getUserListLocked()
	msg := models.Message{
		Type:       models.MsgTypeUserList,
		Users:      users,
		PublicKeys: h.getPublicKeysLocked(),
	}
	for c := range h.Clients {
		if c.Username != "" {
			c.SendJSON(msg)
		}
	}
}

func (h *Hub) sendRoomUsersLocked(room string) {
	members := h.Rooms[room]
	users := make([]string, 0, len(members))
	for c := range members {
		if c.Username != "" {
			users = append(users, c.Username)
		}
	}
	msg := models.Message{
		Type:  models.MsgTypeRoomUsers,
		Room:  room,
		Users: users,
	}
	for c := range members {
		if c.Username != "" {
			c.SendJSON(msg)
		}
	}
}
