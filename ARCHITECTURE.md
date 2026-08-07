# WiFi Chat — Architecture & Design

## Overview

WiFi Chat is a real-time, end-to-end encrypted, mesh-networked chat application built with Go (backend) and vanilla JavaScript (frontend). It enables group chat, encrypted direct messaging, voice/video calls, and image sharing over a local WiFi network without internet connectivity. Multiple nodes can form a mesh network for extended range.

### System Diagram

```
┌──────────────────────────── WiFi Network ────────────────────────────┐
│                                                                      │
│  ┌────────────────── Node 1 ──────────────────┐                      │
│  │                                             │                      │
│  │  cmd/main.go                                │                      │
│  │    │                                        │                      │
│  │    ▼                                        │                      │
│  │  server.Start()                             │                      │
│  │    ├── HTTP FileServer (port 8000)          │                      │
│  │    ├── /ws (client WebSocket)               │                      │
│  │    ├── /ws/mesh (mesh peer WebSocket)       │                      │
│  │    ├── /api/upload (image upload)           │                      │
│  │    └── discovery.Start() (UDP:19540)        │                      │
│  │                                 ▼           │                      │
│  │    ┌─────────────────────────────────┐      │   ┌──────────────┐  │
│  │    │          HUB                    │◄─────┼───│ Phone (WS)   │  │
│  │    │  • Clients    • Rooms           │      │   │ E2EE keys    │  │
│  │    │  • UserClients • MessageStore   │◄─────┼───│ Laptop (WS)  │  │
│  │    │  • Mesh (gossip + user sync)    │      │   │ E2EE keys    │  │
│  │    └──────────┬──────────────────────┘      │   └──────────────┘  │
│  │               │                             │                      │
│  └───────────────┼─────────────────────────────┘                      │
│                  │ mesh WS                                            │
│                  ▼                                                    │
│  ┌────────────── Node 2 (relay) ──────────────┐                      │
│  │  Same structure — auto-discovered via UDP  │                      │
│  │  DMs pass through encrypted (can't read)   │                      │
│  └────────────────────────────────────────────┘                      │
└──────────────────────────────────────────────────────────────────────┘
```

---

## Core Components

### 1. Entry Point — `cmd/main.go`

**Purpose:** Application bootstrap and CLI flag parsing.

**CLI Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | `8000` | HTTP server port |
| `-node` | hostname | Mesh node identifier |
| `-peer` | none | Manual upstream peer URL (e.g., `ws://192.168.1.10:8000/ws/mesh`) |

**Flow:**
```
main()
  ├── Parse flags: -port, -node, -peer
  ├── NewServer(port, nodeName)
  │    ├── NewMessageStore("./data")     // JSONL persistence
  │    ├── NewHub(store)                 // Central router
  │    ├── mesh.New(nodeName)            // Mesh networking
  │    └── discovery.New(nodeName, port) // UDP auto-discovery
  ├── server.Start()
  │    ├── go hub.Run()
  │    ├── discovery.Start()
  │    └── http.ListenAndServe()
  └── If -peer flag: server.ConnectPeer(url)
```

---

### 2. Server — `internal/server/server.go`

**Purpose:** HTTP server, WebSocket upgrade, image upload API, and component orchestration.

**Structure:**
```go
type Server struct {
    Hub        *hub.Hub
    Store      *store.MessageStore
    Mesh       *mesh.Mesh
    Discovery  *discovery.Discovery
    Port       int
    HTTPServer *http.Server
}
```

**HTTP Routes:**

| Route | Method | Purpose |
|-------|--------|---------|
| `/` | GET | Serve frontend static files |
| `/ws` | GET | Client WebSocket upgrade |
| `/ws/mesh` | GET | Mesh peer WebSocket upgrade |
| `/api/upload` | POST | Image upload (max 10MB, returns URL) |
| `/uploads/*` | GET | Serve uploaded images |

**WSL2 Detection:**
- Checks `/proc/version` for "microsoft" or "WSL"
- Reads Windows host IP from `/etc/resolv.conf`
- Prints port-forwarding instructions for phone access

---

### 3. Client — `internal/hub/client.go`

**Purpose:** Represents a single WebSocket connection with two goroutine pumps.

**Structure:**
```go
type Client struct {
    Hub       *Hub
    Conn      *websocket.Conn
    Send      chan []byte       // Buffered (256) outbound queue
    Username  string            // Set on "join"
    PublicKey string            // E2EE public key (base64)
    Rooms     map[string]bool   // Room memberships
    closed    bool              // Prevents double-close panic
    mu        sync.RWMutex
}
```

**Goroutine Pumps:**

| Pump | Direction | Behavior |
|------|-----------|----------|
| ReadPump | WS → Hub | Reads JSON, calls `Hub.ProcessMessage()`. On error, sends to `Unregister` channel. |
| WritePump | Hub → WS | Drains `Send` channel, writes to WS. Pings every 54s, expects pong within 60s. |

**Safety:**
- `CloseSend()` — Idempotent close of the `Send` channel (prevents double-close panic on reconnect)
- `SendJSON()` — Checks `closed` flag before writing to channel

---

### 4. Hub — `internal/hub/hub.go`

**Purpose:** Central message router, room manager, and state owner.

**Structure:**
```go
type Hub struct {
    Clients      map[*Client]bool            // All connected clients
    UserClients  map[string]*Client           // Username → Client
    Rooms        map[string]map[*Client]bool  // Room → members
    RemoteUsers  map[string]string            // Remote username → peer node ID
    Register     chan *Client
    Unregister   chan *Client
    Store        *store.MessageStore
    Mesh         *mesh.Mesh
    mu           sync.RWMutex
}
```

**Event Loop (`Run()`):**

```
Register  → Add client to Clients map
Unregister →
  ├── Check if client is CURRENT owner of username
  │   (prevents "user left" on reconnect when old conn is garbage-collected)
  ├── If current owner: broadcast user_left, update user list, sync mesh
  └── If replaced (reconnect): silently remove, no broadcast
```

**Reconnect Handling:**
When a user refreshes the page, the old WebSocket may not have disconnected yet. The hub handles this by:
1. Detecting the existing username in `handleJoin`
2. Setting `isReconnect = true`
3. Kicking the old client silently (no "user left" broadcast)
4. Skipping "user joined" broadcast for reconnects
5. In `Unregister`, comparing the client pointer to the current owner to avoid stale broadcasts

**Message Processing (`ProcessMessage()`):**

| Type | Handler | Behavior |
|------|---------|----------|
| `join` | `handleJoin` | Validate username, assign public key, auto-join #general, send room/user lists + history, broadcast user_joined (new logins only) |
| `message` | `handleMessage` | Verify room membership, store message, broadcast to room, propagate via mesh |
| `dm` | `handleDM` | Find target (local or remote), store DM (preserving `encrypted` flag), send to target + echo to sender, propagate via mesh if remote |
| `image` | `handleImage` | Same as `message` but with `image_url` field |
| `create_room` | `handleCreateRoom` | Create room, auto-join creator, broadcast updated room list |
| `join_room` | `handleJoinRoom` | Add to room, send history, broadcast user_joined |
| `leave_room` | `handleLeaveRoom` | Remove from room (except #general), broadcast user_left |
| `typing` | `handleTyping` | Broadcast to room (exclude sender) |
| `get_dm_history` | `handleGetDMHistory` | Return last 50 DMs between two users (preserving `encrypted` flag) |
| `key_exchange` | `handleKeyExchange` | Forward E2EE public key to target user |
| `call_*` | `handleCallSignal` | Forward WebRTC signaling (offer/answer/ICE/etc) to target |

---

### 5. Message Store — `internal/store/store.go`

**Purpose:** JSONL file-based persistent message storage with in-memory cache.

**Structure:**
```go
type StoredMessage struct {
    ID        int64     `json:"id"`
    Room      string    `json:"room"`
    From      string    `json:"from"`
    To        string    `json:"to,omitempty"`
    Content   string    `json:"content"`
    ImageURL  string    `json:"image_url,omitempty"`
    Timestamp time.Time `json:"ts"`
    IsDM      bool      `json:"is_dm"`
    Encrypted bool      `json:"encrypted,omitempty"`
}
```

**Persistence:**
- Messages are appended to `data/messages.jsonl` as one JSON object per line
- On startup, the store reads the entire file to restore in-memory state
- Thread-safe with `sync.RWMutex`

**Operations:**

| Method | Purpose |
|--------|---------|
| `SaveMessage(msg)` | Store a room message (file + memory) |
| `SaveDM(msg)` | Store a direct message (preserves encrypted flag) |
| `GetRoomMessages(room, limit)` | Last N messages for a room |
| `GetDMs(user1, user2, limit)` | DM history between two users |

---

### 6. Message Protocol — `internal/models/message.go`

**JSON Envelope:**
```go
type Message struct {
    // Core fields
    Type       MessageType       `json:"type"`
    Content    string            `json:"content,omitempty"`
    Room       string            `json:"room,omitempty"`
    From       string            `json:"from,omitempty"`
    To         string            `json:"to,omitempty"`
    Username   string            `json:"username,omitempty"`
    Timestamp  int64             `json:"ts,omitempty"`
    Rooms      []string          `json:"rooms,omitempty"`
    Users      []string          `json:"users,omitempty"`
    Messages   []Message         `json:"messages,omitempty"`
    Error      string            `json:"message,omitempty"`
    ImageURL   string            `json:"image_url,omitempty"`
    // WebRTC / E2EE
    SDP        string            `json:"sdp,omitempty"`
    Candidate  interface{}       `json:"candidate,omitempty"`
    CallType   string            `json:"call_type,omitempty"`
    ID         string            `json:"id,omitempty"`
    PublicKey  string            `json:"public_key,omitempty"`
    Encrypted  bool              `json:"encrypted,omitempty"`
    PublicKeys map[string]string `json:"public_keys,omitempty"`
    // Multi-hop distributed mesh fields (Android MeshRouter interop)
    MessageID  string   `json:"message_id,omitempty"`
    OriginNode string   `json:"origin_node,omitempty"`
    TTL        int      `json:"ttl,omitempty"`
    Hops       int      `json:"hops,omitempty"`
    HopPath    []string `json:"hop_path,omitempty"`
    NodeID     string   `json:"node_id,omitempty"`
    // Delivery receipts
    Status     string   `json:"status,omitempty"`
    RefID      string   `json:"ref_id,omitempty"`
    // Peer topology (announce messages)
    Neighbors  []string `json:"neighbors,omitempty"`
    // Store-and-forward
    QueuedFor  string   `json:"queued_for,omitempty"`
}
```

**Message Types:**

| Type | Direction | Purpose |
|------|-----------|--------|
| `join` | C→S | Join with username + E2EE public key |
| `message` | C↔S↔Mesh | Room message (carries `message_id` for tracking) |
| `dm` | C↔S↔Mesh | Direct message (may be encrypted, carries `message_id`) |
| `image` | C→S | Image with optional caption |
| `create_room` | C→S | Create new room |
| `join_room` | C→S | Join existing room |
| `leave_room` | C→S | Leave room |
| `typing` | C↔S | Typing indicator |
| `get_dm_history` | C→S | Request DM history with a peer |
| `dm_history` | S→C | DM history response |
| `key_exchange` | C↔S↔Mesh | E2EE public key exchange |
| `call_request/accept/reject` | C↔S↔Mesh | Call signaling |
| `call_offer/answer/ice` | C↔S↔Mesh | WebRTC SDP/ICE exchange |
| `call_end` | C↔S↔Mesh | End call |
| `user_joined` | S→C | User joined (includes public key) |
| `user_left` | S→C | User left |
| `room_list` | S→C | All rooms |
| `user_list` | S→C | Online users + public keys |
| `room_users` | S→C | Users in a specific room |
| `history` | S→C | Room message history |
| `system` | S→C | System notification |
| `error` | S→C | Error message |
| `mesh_sync` | Node↔Node | User list sync (legacy, Go compat) |
| `announce` | Node↔Node | Topology advertisement (node_id, users, neighbors) |
| `delivery_ack` | Node↔Node | Delivery receipt (ref_id, status="delivered") |
| `read_receipt` | Node↔Node | Read receipt (ref_id, status="read") |
| `store_forward` | Node↔Node | Queued message delivery for offline users |
| `peer_discovery` | Node↔Node | Peer discovery request |

---

### 7. Mesh Networking — `internal/mesh/mesh.go`

**Purpose:** Peer-to-peer mesh for multi-node message relay, user synchronization, and multi-hop TTL-based forwarding. Interoperable with Android MeshRouter.

**Structure:**
```go
type Mesh struct {
    NodeID        string
    peers         map[string]*Peer         // Connected mesh peers
    connectedURLs map[string]bool          // Prevents duplicate connections
    seen          map[string]bool          // Message deduplication
    remoteUsers   map[string][]string      // peerID → usernames
    localUsers    []string                 // Users on this node
    outbox        map[string][]string      // targetUser → queued JSON
    OnMessageFromPeer func(msg Message)    // Callback: message from mesh
    OnUsersFromPeer   func(peerID string, users []string)
}
```

**Constants:**

| Constant | Value | Purpose |
|----------|-------|--------|
| `MaxTTL` | 7 | Maximum hop count for multi-hop relay |
| `AnnounceInterval` | 10s | Topology advertisement frequency |
| `MaxOutboxPerUser` | 100 | Max queued messages per offline user |

**Multi-Hop TTL Relay:**
- Incoming messages with `message_id` are deduped via the `seen` set
- TTL is decremented and hops incremented on each relay
- Self is appended to `hop_path` for routing visibility
- Messages are forwarded to all peers except the ingress (split-horizon)
- Messages with TTL=0 are delivered locally but not forwarded

**Announce Loop:**
- Every 10s, broadcasts `{type: "announce", node_id, users, neighbors, ts}` to all peers
- On receive: updates remote user list, flushes outbox for newly reachable users
- Compatible with Android MeshRouter's announce protocol

**Store-and-Forward Outbox:**
- `EnqueueForUser(user, json)` — queues a message for an unreachable user
- `FlushOutboxForUser(user)` — sends all queued messages when user becomes reachable
- Triggered automatically when an announce reveals new users

**Gossip Protocol:**
- Each message gets a unique ID (`GenerateMsgID()` or `message_id` from Android)
- Before propagating, check `seen` map — skip if already processed
- Forward to all connected peers except the source
- Prevents infinite loops in multi-node topologies

**Peer Connection:**
- WebSocket at `/ws/mesh`
- Auto-reconnect with 5s backoff
- `ConnectPeer(url)` is idempotent — duplicate calls are ignored via `connectedURLs`

**User Sync:**
- When a user joins/leaves locally, `SyncUsers()` sends user list + `SetLocalUsers()` updates internal state
- Announce messages also carry user lists for topology awareness
- Remote user lists are tracked in both `Hub.RemoteUsers` and `Mesh.remoteUsers`
- Remote users appear in the user list alongside local users

---

### 8. Auto-Discovery — `internal/discovery/discovery.go`

**Purpose:** Zero-configuration peer finding via UDP broadcast.

**How It Works:**
1. Every 3 seconds, broadcast `{node_id, port}` to all LAN broadcast addresses + 255.255.255.255
2. Listen on UDP port 19540 for announcements from other nodes
3. Ignore own announcements (by `node_id`)
4. On new peer: call `OnPeerFound` callback → `mesh.ConnectPeer()`
5. Peers that stop announcing are removed after 10 seconds

**Structure:**
```go
type Announcement struct {
    NodeID string `json:"node_id"`
    Port   int    `json:"port"`
}
```

**Broadcast Address Calculation:**
- Enumerates all non-loopback IPv4 interfaces
- Computes `IP | ~mask` for each to get the broadcast address

---

### 9. Frontend — `frontend/`

#### `app.js` — Main Application

**State Management:**
```javascript
let ws                  // WebSocket connection
let myUsername           // Current user's name
let currentView          // { type: 'room'|'dm', name: string }
let messageStore         // key → [messages] (persisted in sessionStorage)
let unreadCounts         // key → unread count
let tabFocused           // Tab visibility for title badge
let totalUnread          // Total unread for tab title
```

**Session Persistence (sessionStorage):**

| Key | Purpose |
|-----|---------|
| `wifi-chat-username` | Auto-login on refresh |
| `wifi-chat-view` | Restore current room/DM on refresh |
| `wifi-chat-messages` | Full message history (survives refresh) |
| `e2ee-privkey` | ECDH private key (JWK) |
| `e2ee-pubkey` | ECDH public key (JWK) |
| `e2ee-pubkey-b64` | Public key in base64 (for sending) |
| `e2ee-peers` | Map of peer → { publicKey, sharedKey } |

**Features:**
- **Browser Notifications** — Native OS notifications via Notification API (no third-party service, works on LAN):
  - Messages: sender name + content preview when tab is not focused
  - Calls: caller name + call type (voice/video) with ringtone
  - Click notification to focus tab and switch to relevant chat
  - Auto-close after 5 seconds; permission requested on login
- Notification sound (Web Audio API oscillator) for messages in other chats
- Ringtone (two-tone pattern) for incoming calls
- Tab title unread badge: `(3) WiFi Chat`
- Scroll-to-bottom button when scrolled up
- Image upload with preview and lightbox
- Emoji picker with categorized grid
- DM encryption/decryption with key persistence

#### `crypto.js` — End-to-End Encryption

**Algorithm:** ECDH P-256 key exchange → AES-256-GCM symmetric encryption

**Flow:**
```
init()
  ├── Check sessionStorage for existing keys
  │   ├── Found → import from JWK
  │   └── Not found → generate new ECDH P-256 key pair
  └── Export public key as base64 for sending to server

importPeerKey(username, base64PublicKey)
  ├── Import peer's ECDH public key
  ├── Derive shared AES-256-GCM key via ECDH
  └── Store in sessionStorage: { publicKey, sharedKey }

encrypt(username, plaintext)
  ├── Lookup shared key for peer
  ├── Generate random 12-byte IV
  ├── AES-GCM encrypt
  └── Return base64(IV + ciphertext)

decrypt(username, base64data)
  ├── Lookup shared key for peer
  ├── Split IV (first 12 bytes) from ciphertext
  └── AES-GCM decrypt → plaintext
```

#### `call.js` — Voice/Video Calling

**Technology:** WebRTC peer-to-peer with server-relayed signaling

**Call Flow:**
```
Caller                    Server                    Callee
  │ call_request ──────────►│──────────► call_request  │
  │                         │                          │
  │                         │◄────────── call_accept   │
  │ call_accept ◄───────────│                          │
  │                         │                          │
  │ call_offer ────────────►│──────────► call_offer    │
  │                         │                          │
  │                         │◄────────── call_answer   │
  │ call_answer ◄───────────│                          │
  │                         │                          │
  │ ◄─────── ICE candidates exchanged ───────►         │
  │                         │                          │
  │ ◄════════ P2P media stream (WebRTC) ══════►        │
```

**Features:**
- Voice and video call modes
- Mute/unmute microphone
- Camera on/off toggle
- Call timer
- Incoming call modal with accept/reject
- E2EE encrypted signaling (call setup data)

---

## Concurrency Model

```
Main Goroutine
  │
  ├── server.Start()
  │    │
  │    ├── go hub.Run()                  // Single goroutine: state owner
  │    │    ├── Register channel         // New connections
  │    │    └── Unregister channel       // Disconnections (smart reconnect)
  │    │
  │    ├── go discovery.broadcast()      // UDP announcer (every 3s)
  │    ├── go discovery.listen()         // UDP listener (port 19540)
  │    ├── go discovery.cleanup()        // Stale peer reaper (every 10s)
  │    │
  │    └── http.ListenAndServe()         // HTTP server
  │         └── Per WebSocket connection:
  │              ├── go client.ReadPump()    // WS → Hub
  │              └── go client.WritePump()   // Hub → WS
  │
  └── Per mesh peer:
       ├── go mesh.ConnectPeer()         // Outbound (with auto-reconnect)
       └── go peer.readLoop()            // Inbound messages
```

**Synchronization:**
- `Hub.mu` (`sync.RWMutex`) protects `Clients`, `Rooms`, `UserClients` maps
- `Client.mu` protects the `closed` flag and room membership
- `Mesh.peerMu` protects the peers map
- `Mesh.connectedURLsMu` protects the URL dedup map
- `Mesh.seenMu` protects the message dedup set
- `Store.mu` protects the in-memory message slice and file writes
- `Client.Send` is a buffered channel (256) — prevents blocking the hub

---

## E2EE Data Flow (DM)

```
Alice's Browser                       Server/Relay                    Bob's Browser
───────────────                       ────────────                    ─────────────
     │                                     │                              │
     │  1. Generate ECDH key pair          │                              │
     │  2. Send public key in "join"       │                              │
     │  ───────────────────────────►       │                              │
     │                                     │  3. Forward public key       │
     │                                     │  ──────────────────────────► │
     │                                     │                              │
     │                                     │  4. Bob sends his pub key    │
     │                                     │  ◄────────────────────────── │
     │  5. Receive Bob's public key        │                              │
     │  ◄───────────────────────────       │                              │
     │                                     │                              │
     │  6. ECDH derive shared key          │         7. Same shared key   │
     │  7. AES-GCM encrypt("hello")        │                              │
     │  8. Send encrypted ciphertext       │                              │
     │  ───────────────────────────►       │                              │
     │                          ┌──────────┤                              │
     │                          │ Server   │  9. Forward ciphertext       │
     │                          │ CANNOT   │  ──────────────────────────► │
     │                          │ decrypt  │                              │
     │                          └──────────┤  10. AES-GCM decrypt         │
     │                                     │      → "hello" ✓            │
```

---

## Session & Reconnect Flow

```
Page Refresh
  │
  ├── Browser: loadMessageStore() from sessionStorage
  ├── Browser: restore currentView from sessionStorage
  ├── Browser: restore E2EE keys from sessionStorage
  ├── Browser: auto-login with saved username
  │
  └── Server: handleJoin()
       ├── Detect existing username → isReconnect = true
       ├── Kick old client silently (CloseSend)
       ├── Skip "user joined" broadcast
       ├── Send room list, user list, history
       └── Old client's Unregister fires:
            └── Check client != current owner → skip "user left"
```

---

## File Structure

```
wifi-chat/
├── cmd/
│   └── main.go                    # Entry point, CLI flags
├── internal/
│   ├── discovery/
│   │   └── discovery.go           # UDP broadcast auto-discovery
│   ├── hub/
│   │   ├── hub.go                 # Central message router
│   │   └── client.go              # WebSocket client + pumps
│   ├── mesh/
│   │   └── mesh.go                # Peer-to-peer mesh networking
│   ├── models/
│   │   └── message.go             # Message types + JSON envelope
│   ├── server/
│   │   └── server.go              # HTTP server + WS upgrade
│   └── store/
│       └── store.go               # JSONL file persistence
├── frontend/
│   ├── index.html                 # Single-page chat UI
│   ├── app.js                     # WS client, UI, session mgmt
│   ├── crypto.js                  # E2EE (ECDH + AES-GCM)
│   ├── call.js                    # WebRTC voice/video
│   ├── style.css                  # Dark theme responsive CSS
│   ├── sw.js                      # Service worker (PWA)
│   ├── manifest.json              # PWA manifest
│   └── icons/                     # App icons (192, 512)
├── data/
│   └── messages.jsonl             # Persistent message store
├── uploads/                       # User-uploaded images
├── go.mod
├── go.sum
├── README.md
└── ARCHITECTURE.md                # This file
```

---

## Delivery Receipts

Messages now carry a `message_id` for end-to-end delivery tracking:

```
Sender                    Server/Relay              Recipient
  │                          │                          │
  │  dm {message_id: X}      │                          │
  │ ────────────────────────►│                          │
  │                          │  dm {message_id: X}      │
  │                          │ ────────────────────────►│
  │                          │                          │
  │                          │  delivery_ack            │
  │                          │  {ref_id: X,             │
  │  delivery_ack            │   status: "delivered"}   │
  │ ◄────────────────────────│◄─────────────────────────│
  │                          │                          │
  │                          │  read_receipt            │
  │  read_receipt            │  {ref_id: X,             │
  │ ◄────────────────────────│◄─ status: "read"}       │
```

- Hub auto-sends `delivery_ack` when a DM is delivered to a local client
- `read_receipt` is sent by the client when the user opens the conversation
- Both receipt types are propagated through the mesh for multi-hop scenarios
- The web frontend displays ✓ (sent), ✓✓ (delivered), ✓✓ blue (read)

---

## Android Interoperability

The Go server/web frontend and Android app share the same WebSocket message protocol, E2EE scheme, and WebRTC signaling format. Mobile and desktop users communicate seamlessly.

### Protocol Compatibility

| Feature | Go Server / Web Frontend | Android App |
|---------|--------------------------|-------------|
| **Message JSON envelope** | `models.Message` (30+ fields) | `ChatMessage` with matching `@SerializedName` |
| **Field naming** | `snake_case` JSON tags | `@SerializedName("snake_case")` on camelCase fields |
| **Message types** | 30+ string constants in `models` | Identical constants in `MsgType` object |
| **Dedup ID field** | `id` + `message_id` | `message_id` |
| **User sync** | `mesh_sync` | `announce` (with neighbors) |
| **TTL relay** | ✓ (decrement + forward) | ✓ (same algorithm) |
| **Store-forward** | In-memory outbox | SQLite outbox |
| **Delivery acks** | Auto-generated by server on local DM delivery + mesh delivery | Auto-generated by server on local DM delivery + mesh delivery |
| **Read receipts** | Sent on DM conversation open (`switchToDM`) | Sent on DM conversation open (`switchToDM`) |
| **Topology** | Peer list only | Full topology with neighbors |

### E2EE Cross-Platform

| Aspect | Web (`crypto.js`) | Android (`E2EEManager.kt`) |
|--------|-------------------|---------------------------|
| **Key algorithm** | ECDH P-256 | ECDH secp256r1 (same curve) |
| **Public key format** | Raw uncompressed (65 bytes, `0x04 \|\| x \|\| y`) | Raw uncompressed (65 bytes) — auto-detects X509 from other Android devices |
| **Key exchange** | Base64-encoded raw key in `public_key` field | Same Base64 raw format |
| **Shared secret derivation** | `deriveBits(ECDH, 256)` → `SHA-256` → AES key | `KeyAgreement.ECDH` → `SHA-256` → AES key |
| **Encryption** | AES-256-GCM, random 12-byte IV | AES/GCM/NoPadding, 12-byte IV |
| **Ciphertext format** | `base64(IV \|\| ciphertext)` | `base64(IV \|\| ciphertext)` |
| **Fingerprint** | SHA-256 of raw key → uppercase hex grouped by 4 chars | Same format (verified matching) |

### WebRTC Call Signaling

| Aspect | Web (`call.js`) | Android (`ChatViewModel.kt`) |
|--------|-----------------|------------------------------|
| **Signaling types** | `call_request`, `call_accept`, `call_reject`, `call_offer`, `call_answer`, `call_ice`, `call_end` | Same types via `MsgType` constants |
| **SDP field** | `sdp` (string, optionally E2EE encrypted) | `sdp` (string, optionally E2EE encrypted) |
| **ICE candidate (encrypted)** | `candidate` = Base64 ciphertext string | Same — decrypts to `{sdpMid, sdpMLineIndex, candidate}` JSON |
| **ICE candidate (unencrypted)** | `candidate` = object `{sdpMid, sdpMLineIndex, candidate}` | Same — sends as object (map), receives both object and string formats |
| **Call flow** | Caller sends `call_request` → callee sends `call_accept` → caller creates offer → SDP/ICE exchange | Identical flow |
| **ICE servers** | None (LAN-direct) | None (LAN-direct) |

The Go server handles both `mesh_sync` (legacy Go-to-Go sync) and `announce` (Android topology advertisement) for backward compatibility.

---

## Key Design Decisions

1. **Single Hub Goroutine** — Centralizes state mutations, simplifies concurrency
2. **Channel-based Communication** — Decouples clients from hub, prevents blocking
3. **JSONL File Storage** — Simple, append-only persistence without external DB dependencies
4. **Client-side sessionStorage** — Per-tab session isolation; E2EE keys, messages, and view survive refresh
5. **Smart Reconnect** — Detects page refresh vs new login; suppresses spurious join/left messages
6. **Idempotent CloseSend** — Prevents double-close panics during reconnect race conditions
7. **UDP Auto-Discovery** — Zero-config mesh formation; nodes find each other automatically
8. **Gossip with Dedup** — Messages propagate across mesh with unique IDs to prevent loops
9. **E2EE with Key Persistence** — ECDH keys stored in sessionStorage; derived shared keys cached
10. **Vanilla JavaScript** — No build tools, no npm; works directly in any browser
11. **Progressive Web App** — Installable on mobile, offline-capable for static assets
12. **Message Envelope Pattern** — Single JSON structure for all 30+ message types
13. **Multi-Hop TTL Relay** — Messages carry TTL and hop_path for visibility; split-horizon prevents loops
14. **Announce-Based Topology** — Periodic topology ads enable store-forward flush on peer discovery
15. **Delivery Receipts** — message_id enables end-to-end delivery + read confirmation
16. **Android Interop** — Dual protocol support (mesh_sync + announce) for Go ↔ Android mesh
17. **Public Keys in Broadcasts** — User list updates always include E2EE public keys, not just on initial join
18. **Remote DM Persistence** — DMs to remote mesh users are stored locally before propagation for history continuity
19. **Persistent Message Status** — Delivery/read receipts update stored message status via `UpdateDMStatus`, so `dm_history` reflects true state
20. **Cross-Mesh E2EE Key Exchange** — `key_exchange` messages propagate via mesh, enabling E2EE between users on different mesh nodes
21. **Mesh Typing Indicators** — Typing events from mesh peers are broadcast to local room members
22. **Image DM Pre-Store** — Web image DMs are pre-stored locally with `message_id` and `status: 'sent'` (server echo is skipped for own DMs)
23. **Slash Command DM Visibility** — `/slap` and `/hug` DMs are pre-stored so the sender sees their own message
24. **Android Auto-Host Sync** — Android app now auto-starts server on login (no host/client choice). Go server already runs this way via CLI. Both platforms auto-discover and mesh-connect.
25. **Android Server Shutdown** — Android users can shut down their mesh node via "Stop Server & Logout" in sidebar. Go server uses Ctrl+C / SIGTERM.
26. **Announce All Known Users** — `broadcastAnnounce` sends all known users (local + transitive remote) so multi-hop peer chains discover the full user set
27. **Announce Forwarding** — Received announce messages are forwarded to other peers with TTL decrement + dedup + split-horizon, enabling topology propagation beyond direct connections
28. **Hub Announce Integration** — `handleAnnounce` calls `OnUsersFromPeer` callback, ensuring hub's `RemoteUsers` map and web client user lists update from announce-discovered peers
