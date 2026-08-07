# WiFi Chat

A real-time, end-to-end encrypted, mesh-networked chat application that works over local WiFi — **no internet required**. Built with Go (WebSocket backend) and vanilla JavaScript frontend. Designed for scenarios like protests, field operations, disaster response, or any situation where you need private communication without centralized infrastructure.

## Features

### Core Chat
- **Group Chat** — Multiple rooms, create/join/leave
- **Direct Messages** — Private 1-on-1 conversations with E2EE
- **Real-time** — Instant message delivery via WebSockets
- **Typing Indicators** — See when someone is typing
- **Message History** — Persistent JSONL file storage + client-side sessionStorage
- **Online Presence** — See who's connected
- **Image Sharing** — Upload and share images with captions
- **Emoji Picker** — Rich emoji selection with categories
- **Push Notifications** — Native OS notifications for messages and calls (Browser Notification API)
- **Notification Sound** — Audio alerts for new messages in other chats
- **Ringtone** — Distinct audio pattern for incoming calls
- **Unread Badges** — Tab title and sidebar show unread counts

### Security
- **End-to-End Encryption (E2EE)** — ECDH P-256 key exchange + AES-256-GCM for DMs
- **Cross-Platform E2EE** — Shared ECDH + SHA-256 key derivation compatible with Android app
- **Key Persistence** — Crypto keys survive page refresh (stored in sessionStorage)
- **Relay-Safe** — Relay nodes only see encrypted ciphertext for DMs
- **No Accounts** — Username-only, no passwords to leak
- **Rate Limiting** — Server-side message throttling (max 30 msg/sec per user)
- **Username Validation** — Alphanumeric + `_-.` only, 1–32 characters
- **Content Length Limits** — Messages capped at 10,000 characters
- **Cryptographic Message IDs** — Uses `crypto/rand` for unpredictable deduplication IDs
- **In-Memory Store Cap** — Message store capped at 50,000 entries to prevent memory exhaustion
- **Emergency Wipe** — `/wipe` command instantly clears all local data (BitChat-inspired)
- **Key Fingerprints** — `/fingerprint` shows SHA-256 fingerprint for in-person contact verification (Briar-inspired)
- **Disappearing Messages** — `/disappear [minutes]` auto-deletes messages after timeout (Briar-inspired)

### Code Detection & Syntax Highlighting
- **Auto-Detect** — Automatically detects pasted code (Go, Python, JS, Kotlin, Rust, Java, C/C++, Bash, SQL, etc.)
- **Fenced Code Blocks** — Supports ` ```lang ` markers for explicit language tagging
- **Inline Code** — Backtick-wrapped `` `code` `` renders in monospace
- **IDE-Like Theme** — One Dark-inspired syntax colors (keywords, strings, comments, functions)
- **Copy Button** — One-click copy for code blocks
- **Cross-Platform** — Same rendering on Web and Android

### Voice & Video Calling
- **Voice Calls** — WebRTC peer-to-peer audio
- **Video Calls** — WebRTC peer-to-peer video
- **Call UI** — Mute/unmute, camera toggle, call timer
- **Encrypted Signaling** — Call setup data encrypted end-to-end
- **Cross-Platform Calls** — Web ↔ Android voice/video interop via compatible WebRTC signaling

### Mesh Networking
- **Auto-Discovery** — Nodes automatically find each other via UDP broadcast
- **Multi-Hop Relay** — Messages carry TTL (max 7 hops) and traverse intermediate relay nodes
- **Gossip Protocol** — Messages propagate across the mesh with deduplication by message ID
- **Announce-Based Topology** — Nodes advertise their users and neighbors every 10s
- **Store-and-Forward** — Messages for unreachable users are queued and delivered when they come online
- **Delivery Receipts** — ✓ sent, ✓✓ delivered, ✓✓ (blue) read — tracked via `message_id`
- **Android Interop** — Full interoperability with WiFi Chat Android app's MeshRouter
- **Transparent to Users** — No manual `--peer` flags needed; just run the app

### Progressive Web App
- **PWA Support** — Installable on mobile home screens
- **Service Worker** — Offline caching of static assets
- **Mobile Responsive** — Full sidebar, touch-friendly UI

## Quick Start

```bash
# Build
cd wifi-chat
go build -o wifi-chat ./cmd/main.go

# Run (auto-discovers other nodes on the network)
./wifi-chat

# Or with a custom port and node name
./wifi-chat -port 9000 -node kitchen-node
```

The server prints the local network URL:

```
╔══════════════════════════════════════════════════════╗
║             WiFi Chat Server Started!               ║
╠══════════════════════════════════════════════════════╣
║  Local:   http://localhost:8000                      ║
║  Network: http://192.168.1.42:8000                   ║
╠══════════════════════════════════════════════════════╣
║  Share the Network URL with people on your WiFi      ║
╚══════════════════════════════════════════════════════╝
```

Share the Network URL with anyone on the same WiFi. They open it in any browser and start chatting.

## Mesh Networking

### How It Works

Every device running WiFi Chat is a **mesh node**. Nodes automatically discover each other via UDP broadcast on port 19540 and connect via WebSocket.

```
         WiFi Range                WiFi Range
     ┌───────────────┐        ┌───────────────┐
     │   User A      │        │   User C      │
     │ (phone)       │        │ (phone)       │
     │ connects to   │        │ connects to   │
     │ Node 1        │        │ Node 3        │
     └───────┬───────┘        └───────┬───────┘
             │                        │
     ┌───────▼───────┐        ┌───────▼───────┐
     │   Node 1      │◄──────►│   Node 3      │
     │ (laptop)      │  mesh  │ (laptop)      │
     └───────┬───────┘        └───────┬───────┘
             │                        │
             │        ┌───────┐       │
             └───────►│Node 2 │◄──────┘
                      │(relay)│
                      └───────┘
```

**User A** sends a DM to **User C**:
1. Message is E2EE encrypted on User A's browser
2. Node 1 forwards encrypted message to Node 2 (relay)
3. Node 2 forwards to Node 3 — **Node 2 cannot read the message**
4. User C's browser decrypts with the shared key

### Manual Peering (optional)

For nodes on different subnets or across network boundaries:

```bash
# On Node 2, manually connect to Node 1
./wifi-chat -port 8000 -peer ws://192.168.1.10:8000/ws/mesh
```

## Android Interoperability

WiFi Chat is fully interoperable with the [WiFi Chat Android](../wifi-chat-android) app. Mobile and desktop users communicate seamlessly — same protocol, same encryption, same call signaling.

| Scenario | Status |
|----------|--------|
| Go server ↔ Android mesh peer | ✓ |
| Go server ↔ Web browser | ✓ |
| Android ↔ Android mesh | ✓ |
| Android ↔ Web browser (via Go server) | ✓ |
| Multi-hop: Android → Go → Android | ✓ |
| E2EE DMs: Web ↔ Android | ✓ |
| Voice/Video calls: Web ↔ Android | ✓ |
| Key fingerprint verification: Web ↔ Android | ✓ |
| Delivery/read receipts: Web ↔ Android | ✓ |

**Verified Cross-Platform:**
- **Protocol:** All 30+ message types and JSON field names match exactly between platforms
- **E2EE:** ECDH P-256 raw key exchange + SHA-256 KDF + AES-256-GCM — identical on both platforms; public keys included in all user list broadcasts
- **Calls:** WebRTC signaling (SDP/ICE) with encrypted and unencrypted candidate formats handled on both sides
- **Mesh:** `announce` topology, TTL relay, store-and-forward, delivery receipts — interoperable; MeshManager relays all message types including receipts and key exchange
- **Receipts:** Server auto-generates `delivery_ack` on local DM delivery (both Go and Android Hub); both platforms send `read_receipt` on conversation open
- **Fingerprints:** SHA-256 of raw public key, formatted as uppercase hex groups of 4 chars — identical output
- **History:** Remote DMs stored before mesh propagation ensuring history survives page refresh

The Go server understands both legacy `mesh_sync` (user list sync) and new `announce` (topology advertisement) message types, ensuring backward compatibility.

## End-to-End Encryption

DMs use **ECDH P-256 key exchange** + **AES-256-GCM** encryption:

1. Each browser generates an ECDH key pair on login
2. Public keys are exchanged during the `user_joined` handshake
3. Raw ECDH shared secret is derived, then hashed with **SHA-256** to produce the AES key
4. Messages are encrypted with AES-256-GCM (12-byte IV prepended) before leaving the browser
5. The server (and relay nodes) only store/forward encrypted ciphertext
6. Keys persist in `sessionStorage` — survive page refresh within the same tab

**Cross-Platform Interop:** The key derivation (ECDH → SHA-256 → AES-256) matches the Android app's `E2EEManager`, and public keys are exchanged in raw uncompressed format (65 bytes: `0x04 || x || y`), ensuring DM encryption works seamlessly between Go web and Android clients.

**Limitation:** Opening a new tab/browser creates new keys. Old encrypted messages from a previous session can't be decrypted (shown as 🔒).

## IRC-Style Slash Commands (BitChat + Briar inspired)

| Command | Alias | Description |
|---------|-------|-------------|
| `/who` | `/w` | List online users |
| `/msg @user [text]` | `/m` | Open DM with user, optionally pre-fill message |
| `/clear` | — | Clear current chat locally |
| `/slap @user` | — | Slap someone with a trout 🐟 |
| `/hug @user` | — | Give someone a hug 🫂 |
| `/fingerprint [@user]` | `/fp` | Show key fingerprint for identity verification |
| `/disappear [minutes]` | — | Auto-delete messages after timeout (0 = off) |
| `/wipe` | — | Emergency wipe all local data |
| `/help` | — | Show command help |

## Architecture

```
wifi-chat/
├── cmd/
│   └── main.go                   # Entry point, CLI flags
├── internal/
│   ├── discovery/
│   │   └── discovery.go          # UDP broadcast auto-discovery
│   ├── hub/
│   │   ├── hub.go                # Central message router + room manager
│   │   └── client.go             # WebSocket client (read/write pumps)
│   ├── mesh/
│   │   └── mesh.go               # Peer-to-peer mesh networking
│   ├── models/
│   │   └── message.go            # Message types and JSON structures
│   ├── server/
│   │   └── server.go             # HTTP server, WS upgrade, image upload
│   └── store/
│       └── store.go              # JSONL file-based message persistence
├── frontend/
│   ├── index.html                # Single-page chat app
│   ├── app.js                    # WebSocket client, UI, session mgmt
│   ├── crypto.js                 # E2EE (ECDH + AES-GCM)
│   ├── call.js                   # WebRTC voice/video calling
│   ├── codeformat.js             # Code detection & syntax highlighting
│   ├── style.css                 # Dark theme responsive styles
│   ├── sw.js                     # Service worker for PWA
│   └── manifest.json             # PWA manifest
├── data/
│   └── messages.jsonl            # Persistent message store
├── uploads/                      # User-uploaded images
├── go.mod
├── README.md
└── ARCHITECTURE.md               # Detailed technical docs
```

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | `8000` | HTTP server port |
| `-node` | auto | Node name for mesh identification |
| `-peer` | none | Manual upstream peer URL(s), comma-separated |

## How It Works

1. One or more devices run WiFi Chat (each is a mesh node)
2. Nodes auto-discover each other via UDP broadcast on port 19540
3. Users open the Network URL in any browser and pick a username
4. Group messages route through the local node and propagate via mesh
5. DMs are encrypted end-to-end — relay nodes only see ciphertext
6. Chat history persists in `data/messages.jsonl` and in the browser's sessionStorage

## WSL2 Users

The server detects WSL2 and prints port-forwarding instructions:

```powershell
# Run in Windows PowerShell (Admin)
netsh interface portproxy add v4tov4 `
  listenport=8000 listenaddress=0.0.0.0 `
  connectport=8000 connectaddress=<WSL_IP>

New-NetFirewallRule -DisplayName "WiFi Chat" `
  -Direction Inbound -LocalPort 8000 -Protocol TCP -Action Allow
```

## Interoperability Audit — Round 2 Fixes

Deep cross-platform audit between Go/Web and Android identified and fixed 14 bugs:

### Message Status Persistence
- **Go `store.go`**: Added `UpdateDMStatus()` — delivery/read receipts now update stored message status
- **Go `hub.go`**: `handleDeliveryReceipt` and `InjectFromMesh` call `UpdateDMStatus` so `dm_history` returns accurate status
- **Go `hub.go`**: `handleDM` updates stored status to "delivered" on local auto-ack

### Mesh Interop
- **Go `hub.go`**: `handleKeyExchange` now propagates via mesh — E2EE key exchange was silently failing for cross-mesh users
- **Go `hub.go`**: `InjectFromMesh` now handles `TYPING` messages from mesh peers

### Web Frontend
- **`app.js`**: Image DMs pre-stored locally with `message_id` and `status: 'sent'` — sender previously never saw their own image DM (server echo skipped)
- **`app.js`**: `/slap` and `/hug` DMs pre-stored for sender visibility
- **`app.js`**: Room image messages now include `message_id` for delivery tracking

## Android Auto-Host & Server Control — Cross-Platform Sync

The Android app has been updated to enforce that every user is automatically a host + relay node (matching Go server behavior), with the ability to shut down the mesh node:

### Android Changes
- **`LoginScreen.kt`**: Removed host/client mode selector ("Host + Chat" / "Join Server"). Users now only enter a username — the server auto-starts on login.
- **`ChatViewModel.kt`**: Removed `isHostMode` and `connectUrl`. `login()` auto-starts the server before connecting. Added `stopServerAndLogout()` for full node shutdown.
- **`MainActivity.kt`**: Simplified composable — removed host mode, port, and connect URL parameters.
- **`Sidebar.kt`**: Added "Stop Server & Logout" button and server address display in footer.
- **`ChatScreen.kt`**: Passes server shutdown and address to sidebar.

### Synchronized Behavior
| Feature | Go Server | Android App |
|---------|-----------|-------------|
| **Auto-start** | Always (CLI launch) | Always (on login) |
| **Node discovery** | UDP broadcast | Android NSD (mDNS) |
| **Shutdown** | Ctrl+C / SIGTERM | "Stop Server & Logout" button |
| **Mesh relay** | Auto-connect to discovered peers | Auto-connect to discovered peers |
| **User choice** | None (always a node) | None (removed host/client selector) |

## Cross-Network Discovery Fixes (Session 6)

8 critical bugs fixed enabling full multi-hop user discovery across different WiFi networks (topology A↔B↔C):

### Go Server
- **`mesh.go`**: `broadcastAnnounce` now announces all known users (local + remote) instead of only local users
- **`mesh.go`**: `readPump` forwards announce messages with TTL+dedup+split-horizon for multi-hop discovery
- **`mesh.go`**: `handleAnnounce` triggers `OnUsersFromPeer` callback so hub's `RemoteUsers` and web client user lists update on announce

### Android (see Android README for full details)
- **`MeshRouter.kt`**: Announces all known users for multi-hop discovery
- **`Hub.kt`**: Dual-path DM/call routing via both MeshManager and MeshRouter
- **`MeshManager.kt`**: Forwards ANNOUNCE messages to other peers

## License

MIT
