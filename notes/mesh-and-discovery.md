# Mesh and Discovery: Complete Architecture Guide

## Overview

WiFi Chat implements a **Layer 3 application-level gossip-based multi-hop mesh relay** that operates on top of existing WiFi connections. It is **NOT** a WiFi MAC-layer implementation (like 802.11s). Instead, it's a software-based protocol running over WebSocket/TCP-IP.

---

## Part 1: Mesh Architecture

### Core Concept

The mesh enables **out-of-range communication** by having intermediate relay nodes forward messages hop-by-hop:

```
A ──WiFi──> B ──WiFi──> C ──WiFi──> D
            (relay)     (relay)
```

A and D may not be in direct WiFi range, but messages can reach from A to D through relays B and C.

### Mesh Components

**Mesh Struct** (`internal/mesh/mesh.go`):

- `peers` — Active WebSocket connections to other mesh nodes
- `seen` — Message deduplication map (msgID → timestamp)
- `remoteUsers` — Users discovered through each peer (peerID → []username)
- `localUsers` — Users connected to this node (set by hub)
- `outbox` — Store-and-forward queue for unreachable users (targetUser → []json)
- `OnMessageFromPeer` — Callback to deliver messages from mesh
- `OnUsersFromPeer` — Callback to handle user presence from mesh

**Peer Struct**:

- `ID` — Peer identifier (URL for outbound, assigned ID for inbound)
- `Conn` — WebSocket connection
- `Send` — Buffered channel for async message sending (256 slots)
- `readPump()` — Receives and processes messages
- `writePump()` — Sends messages and keep-alive pings

### Connection Lifecycle

**Outbound (ConnectPeer)**:

```
1. ConnectPeer(url) called
2. Mark URL as "connected/connecting"
3. Start auto-reconnect goroutine
4. dialPeer():
   - Open WebSocket to peer
   - Send hello (MsgTypeMeshSync)
   - Register in peers map
   - Start readPump() and writePump()
   - Auto-reconnect on disconnect (5s retry)
```

**Inbound (AcceptPeer)**:

```
1. Server receives /ws/mesh WebSocket upgrade
2. AcceptPeer(conn, peerID) called
3. Create Peer struct
4. Register in peers map
5. Start readPump() and writePump()
6. Clean up on disconnect
```

### Message Propagation (Gossip Protocol)

**Deduplication**:

- Uses `message_id` (Android interop) or `id` field
- `seen` map prevents loops with timestamp-based cleanup
- Removes entries older than 5 minutes when map exceeds 10,000 entries

**Multi-Hop Forwarding**:

1. Receive message
2. Check `alreadySeen(id)` — if yes, drop it
3. Mark ID as seen
4. Decrement `TTL` (Time-To-Live, max 7)
5. Increment `Hops` counter
6. Append `NodeID` to `HopPath`
7. Deliver locally (if for this node)
8. Forward to all peers except ingress peer (split-horizon)

**Message Types**:

- `MsgTypeMeshSync` — User presence updates
- `MsgTypeAnnounce` — Topology broadcasts (every 10s)
- Regular messages — Relayed if TTL > 0

### Topology Discovery (Announce Loop)

Every **10 seconds**, each node broadcasts an announce message:

```go
Message {
  Type:      "announce"
  NodeID:    "node-1a2b"
  Users:     ["alice", "bob"]           // local users
  Neighbors: ["ws://192.168.1.11:8000"] // connected peers
  TTL:       7
  HopPath:   ["node-1a2b"]
}
```

**Processing**:

1. Receive announce from peer
2. Dedup by `message_id`
3. Update `remoteUsers[peerID] = msg.Users`
4. Call `OnUsersFromPeer()` callback (hub updates)
5. For each remote user, flush outbox (deliver queued messages)
6. Forward announce to other peers (if TTL > 0)

**Result**: Every node learns which users exist (local + remote) and builds a network topology map.

### User Discovery

```
Node 1                          Node 2
  ├─ local users:                 ├─ local users:
  │  └─ alice                      │  └─ charlie
  │                               │
  └─ Announce every 10s ──────────>
                                  └─ Receives: alice is on Node 1
                                     Updates remoteUsers["node1"] = ["alice"]
                                     Broadcasts alice in own announce

                          Node 3
                            ├─ local users:
                            │  └─ david
                            │
                            └─ Hears from Node 2:
                               alice (remote via Node 1)
                               charlie (local to Node 2)

Result: Node 3 knows about alice, charlie, david
```

**GetAllKnownUsers()**:

- Merges local users + all remote users from all peers
- Deduplicates by username
- Called by hub to update user list for clients

### Store-and-Forward Outbox

For users not yet reachable:

**EnqueueForUser(user, msgJSON)**:

- Add serialized message to `outbox[user]`
- Max 100 messages per user (drops oldest if exceeded)
- Called when routing to unreachable user

**FlushOutboxForUser(user)**:

- Triggered when announce received with user present
- Send all queued messages to all peers
- Clear the queue
- Called by hub when user becomes reachable

---

## Part 2: Discovery Mechanisms

Your code supports **two discovery approaches**:

### 1. Automatic UDP Broadcast Discovery (Primary)

This is the default, automatic, **zero-configuration** approach.

**How it works**:

```
[Each node, every 3 seconds]
  ├─ Get all local interface broadcast addresses
  ├─ Build UDP packet:
  │  └─ { "node_id": "node-1a2b", "port": 8000 }
  ├─ Send to:
  │  ├─ Each interface's broadcast address:19540
  │  └─ 255.255.255.255:19540 (omnibus broadcast)
  └─ Listen on UDP :19540

[On receive]:
  ├─ Parse announcement
  ├─ Check: is this our own node? (ignore if yes)
  ├─ Extract: sender_node_id, sender_port
  ├─ Build WebSocket URL: ws://<sender_ip>:<sender_port>/ws/mesh
  ├─ Check if new peer (not in seen peers map)
  ├─ If new: call OnPeerFound callback
  │  └─ Server auto-connects: Mesh.ConnectPeer(url)
  └─ Update last-seen timestamp

[Every 10 seconds]:
  └─ Remove peers not heard from in 10 seconds
     (they're gone from network)
```

**Key details**:

| Setting                | Value                                               | Notes                               |
| ---------------------- | --------------------------------------------------- | ----------------------------------- |
| **Broadcast port**     | 19540                                               | Hardcoded in `const broadcastPort`  |
| **Broadcast interval** | 3 seconds                                           | Every 3s announce to all interfaces |
| **Peer timeout**       | 10 seconds                                          | Remove if no beacon for 10s         |
| **Max packet size**    | 1024 bytes                                          | UDP payload limit                   |
| **Broadcast targets**  | All interface broadcast addresses + 255.255.255.255 | Covers all subnets on device        |

**Implementation** (`internal/discovery/discovery.go`):

```go
Discovery struct {
    nodeID string
    port int
    peers map[string]time.Time  // nodeID -> last seen
    OnPeerFound func(nodeID, addr string)  // callback
}

Start() {
    go broadcast()    // send UDP every 3s
    go listen()       // receive UDP on :19540
    go cleanup()      // remove stale peers every 10s
}

broadcast() {
    Send { node_id, port } to all broadcast addresses every 3s
}

listen() {
    for each UDP message on :19540:
        if sender != self:
            build: ws://<senderIP>:<senderPort>/ws/mesh
            if new peer:
                OnPeerFound(nodeID, peerAddr)
                    └─> Server calls: Mesh.ConnectPeer(peerAddr)
}
```

**Advantages**:

- ✓ Fully automatic — no user configuration
- ✓ Transparent — users don't see it working
- ✓ Works behind NAT (broadcast is LAN-only)
- ✓ Scales well for local LANs
- ✓ Nodes join/leave dynamically

**Limitations**:

- ✗ LAN-only (broadcast doesn't cross subnets/router boundaries)
- ✗ No inter-subnet mesh connectivity by default

---

### 2. Manual Peering (Secondary — Cross-Subnet)

For nodes on **different WiFi networks** or subnets:

**Usage** (CLI flags in `cmd/main.go`):

```bash
# Single peer
./wifi-chat -port 8000 -peer ws://192.168.1.10:8000/ws/mesh

# Multiple peers (comma-separated)
./wifi-chat -port 8000 -peer ws://192.168.1.10:8000/ws/mesh,ws://10.0.0.100:8000/ws/mesh

# Custom node name + port
./wifi-chat -port 9000 -node kitchen-node -peer ws://192.168.1.10:8000/ws/mesh
```

**Implementation** (`cmd/main.go`):

```go
peer := flag.String("peer", "", "Upstream peer URL (e.g., ws://192.168.1.10:8000/ws/mesh)")
flag.Parse()

if *peer != "" {
    for _, p := range strings.Split(*peer, ",") {
        p = strings.TrimSpace(p)
        if p != "" {
            srv.ConnectPeer(p)  // manually initiate connection
        }
    }
}
```

**How it differs**:

- No UDP discovery
- User must know peer's exact WebSocket endpoint
- Connection initiated immediately on startup
- Still auto-reconnects if peer goes down

---

## Part 3: How Users Operate the App

### Prerequisites Checklist

Before using WiFi Chat for multi-hop:

- [ ] **All phones on same WiFi network** (same SSID, same subnet for auto-discovery)
  - Or: manually specify peers for cross-subnet mesh
- [ ] **App backend running** (Go server with `./wifi-chat`)
- [ ] **App frontend accessible** (browser at `http://localhost:8000` or network URL)
- [ ] **UDP port 19540 not blocked** (for auto-discovery)
- [ ] **WebSocket ports open** (for mesh peer connections, usually automatic)

### Step-by-Step: 4-Phone Multi-Hop Scenario

**Setup Phase**:

1. **Connect all phones to same WiFi**

   ```
   Phones A, B, C, D all connected to same router/hotspot
   Network: 192.168.1.0/24
   ```

2. **Start app on each phone**

   ```bash
   # Phone A (192.168.1.10)
   ./wifi-chat -port 8000 -node node-A

   # Phone B (192.168.1.11)
   ./wifi-chat -port 8000 -node node-B

   # Phone C (192.168.1.12)
   ./wifi-chat -port 8000 -node node-C

   # Phone D (192.168.1.13)
   ./wifi-chat -port 8000 -node node-D
   ```

3. **Auto-discovery happens (no user action)**

   ```
   Each app broadcasts UDP on :19540 every 3s
   All apps hear each other
   All apps auto-connect via WebSocket mesh
   Result: fully connected mesh topology
   ```

4. **Users join the chat app**

   ```
   Browser: http://192.168.1.10:8000
   Enter username: "Alice"
   Click Join

   Browser: http://192.168.1.11:8000
   Enter username: "Bob"
   Click Join

   (same for Charlie, David)
   ```

5. **Mesh synchronizes users**

   ```
   Every 10s, announce loop runs:
   - Node A announces: ["alice"]
   - Node B announces: ["bob"]
   - Node C announces: ["charlie"]
   - Node D announces: ["david"]

   After 10s, all nodes learn about all users
   All browsers show: alice, bob, charlie, david
   ```

**Chat Phase**:

6. **Alice sends message to David** (out of direct range)

   ```
   Alice types: "Hi David!"
   Message JSON:
   {
     "type": "message",
     "from": "alice",
     "to": "david",
     "content": "Hi David!",
     "id": "msg-xyz",
     "ttl": 7,
     "origin_node": "node-A",
     "hop_path": ["node-A"]
   }

   Node A sends to mesh
   ```

7. **Multi-hop relay**

   ```
   Node A → Node B (readPump receives)
   - Check: destination="david", is it me? No
   - TTL 7 → 6, Hops 0 → 1
   - HopPath ["node-A"] → ["node-A", "node-B"]
   - Forward to all peers except A

   Node B → Node C (readPump receives)
   - Check: destination="david", is it me? No
   - TTL 6 → 5, Hops 1 → 2
   - HopPath ["node-A", "node-B"] → ["node-A", "node-B", "node-C"]
   - Forward to all peers except B

   Node C → Node D (readPump receives)
   - Check: destination="david", is it me? No
   - TTL 5 → 4, Hops 2 → 3
   - HopPath [..., "node-C"] → [..., "node-C", "node-D"]
   - Forward to all peers except C

   Node D receives (readPump)
   - Check: destination="david", is it me? YES
   - Deliver to hub via OnMessageFromPeer callback
   - David sees: "Alice (3 hops away): Hi David!"
   ```

8. **David replies**
   ```
   David types: "Hi Alice!"
   Same relay process: Node D → Node C → Node B → Node A
   Alice receives the reply
   ```

---

## Part 4: Real-World Scenarios

### Scenario 1: Same WiFi Network (Auto-Discovery)

```
┌────────────────────────────────────────────┐
│        WiFi Router (192.168.1.0/24)        │
├────────┬─────────────────┬────────┬────────┤
│        │                 │        │        │
│      Phone A           Phone B  Phone C  Phone D
│    (app running)    (app running) ...    ...
│
│   Each app broadcasts UDP:19540 every 3s
│   All apps hear each other → auto-mesh
│
└────────────────────────────────────────────┘

Result: Zero configuration needed
```

**User actions**:

1. Run `./wifi-chat` on each phone
2. Open browser, join as username
3. Start chatting (mesh is automatic)

---

### Scenario 2: Two Networks with Bridge (Manual Peering)

**Problem**: Auto-discovery only works within same WiFi network. To connect two different networks, you need a **bridge node** that physically sits on both networks.

```
Network A (192.168.1.x)          Network B (10.0.0.x)
    WiFi Router A                  WiFi Router B
        │                              │
    Phone A                         Phone B
    192.168.1.10:8000             10.0.0.100:8000
        │                              │
     UDP :19540                    UDP :19540
    (auto-discovery)              (auto-discovery)
        X                              X

    Manual peer connection (WebSocket):
    A --[connects to]--> B
```

**Setup**:

On Phone A (Network A):

```bash
./wifi-chat -port 8000 -node node-A -peer ws://10.0.0.100:8000/ws/mesh
```

→ A connects to B via explicit WebSocket URL

On Phone B (Network B):

```bash
./wifi-chat -port 8000 -node node-B -peer ws://192.168.1.10:8000/ws/mesh
```

→ B connects back to A (bidirectional peer link)

**User joins** (after 10s announcements sync):

Phone A browser: `http://192.168.1.10:8000` → join as "Alice"
Phone B browser: `http://10.0.0.100:8000` → join as "Bob"

**Result**:

- Alice and Bob appear in each other's user lists
- Messages between them relay through WebSocket peer connection
- No auto-discovery needed (explicitly configured via `-peer` flag)

---

### Scenario 3: Multi-Hop Across 3+ Different Networks

**Problem**: For longer chains, you need **bridge nodes** that connect to multiple peers.

```
Network A              Network B              Network C
(192.168.1.x)         (10.0.0.x)            (172.16.0.x)
    │                     │                     │
Phone A (A1)          Phone B (B1)          Phone C (C1)
192.168.1.10:8000    10.0.0.100:8000      172.16.0.100:8000
    │                     │                     │
  Auto-disc            Bridge                Auto-disc
    X                   node                   X
           (connects to 2 peers)

Mesh chain:
    A1 ←→ B1 ←→ C1
    (1)   (2)   (3)

Messages: A1 → B1 (relay) → C1
```

**Key concept**: Node B is the **bridge** that connects to both A and C.

**Setup**:

On Phone A (Network A, auto-discovers nothing):

```bash
./wifi-chat -port 8000 -node node-A -peer ws://10.0.0.100:8000/ws/mesh
```

On Phone B (Network B, **bridge node** connecting to A and C):

```bash
./wifi-chat -port 8000 -node node-B \
  -peer ws://192.168.1.10:8000/ws/mesh,ws://172.16.0.100:8000/ws/mesh
```

→ Note: Two peers separated by comma!

On Phone C (Network C, auto-discovers nothing):

```bash
./wifi-chat -port 8000 -node node-C -peer ws://10.0.0.100:8000/ws/mesh
```

**User joins**:

Phone A: `http://192.168.1.10:8000` → "Alice"
Phone B: `http://10.0.0.100:8000` → "Bob"
Phone C: `http://172.16.0.100:8000` → "Charlie"

**Watch the mesh form** (every 10 seconds):

```
After announce loop (10s):
- A announces: ["alice"]
- B announces: ["bob"]
- C announces: ["charlie"]

Relay happens:
- A sends announce → B receives → B forwards to C
- C sends announce → B receives → B forwards to A
- Result: All three nodes learn all users

All three browsers show:
  alice, bob, charlie (all users in mesh)
```

**Send a message**: Alice to Charlie

```
Alice types: "Hi Charlie!"
Message flow:

  A (readPump):
    - Sends to mesh (TTL=7, HopPath=["node-A"])

  B (readPump):
    - Receives from A (peer from Network A)
    - Checks: destination="charlie", not for me
    - TTL: 7 → 6, HopPath: ["node-A"] → ["node-A", "node-B"]
    - Forwards to all peers except A
    - Sends to C

  C (readPump):
    - Receives message (from B)
    - Checks: destination="charlie", is for me ✓
    - Delivers to hub
    - Charlie sees: "Alice (2 hops): Hi Charlie!"
```

---

### Scenario 4: Multi-Hop Across 4+ Networks (Extended Chain)

For even longer chains, extend the bridge pattern:

```
Network 1        Network 2        Network 3        Network 4
  Phone A          Phone B          Phone C          Phone D
192.168.1.10     10.0.0.100       172.16.0.100     192.0.2.100

  ./wifi-chat      ./wifi-chat      ./wifi-chat      ./wifi-chat
  -node nodeA      -node nodeB      -node nodeC      -node nodeD
  -peer B          -peer A,C        -peer B,D        -peer C

Mesh chain:
A ←→ B ←→ C ←→ D
(1)  (2)  (3)  (4)

Each node knows next hop(s):
- A: "to reach others, go to B"
- B: "connect to both A and C"
- C: "connect to both B and D"
- D: "to reach others, go to C"
```

**Setup commands**:

```bash
# Network 1: Phone A
./wifi-chat -port 8000 -node node-A -peer ws://10.0.0.100:8000/ws/mesh

# Network 2: Phone B (bridge: connects to A and C)
./wifi-chat -port 8000 -node node-B \
  -peer ws://192.168.1.10:8000/ws/mesh,ws://172.16.0.100:8000/ws/mesh

# Network 3: Phone C (bridge: connects to B and D)
./wifi-chat -port 8000 -node node-C \
  -peer ws://10.0.0.100:8000/ws/mesh,ws://192.0.2.100:8000/ws/mesh

# Network 4: Phone D
./wifi-chat -port 8000 -node node-D -peer ws://172.16.0.100:8000/ws/mesh
```

**Message flow** (A to D):

```
A → B (TTL: 7→6, hops: 0→1)
  → C (TTL: 6→5, hops: 1→2)
  → D (TTL: 5→4, hops: 2→3)
  → Delivered (3 hops total)

Return path (D to A):
D → C (TTL: 7→6, hops: 0→1)
  → B (TTL: 6→5, hops: 1→2)
  → A (TTL: 5→4, hops: 2→3)
  → Delivered (3 hops total)
```

**Important**: TTL max is 7. For chains longer than 7 hops, you must increase `MaxTTL` in `internal/mesh/mesh.go`:

```go
const (
    MaxTTL = 7  // ← Change this to 15 for longer chains
    ...
)
```

---

### Scenario 5: Mixed (Auto-Discovery + Manual Peering)

Combine auto-discovery within networks + manual peering across networks:

```
Subnet A (192.168.1.x) - Auto-discovery
    Phone A1 ←→ Phone A2 ←→ Phone A3

Subnet B (10.0.0.x) - Auto-discovery
    Phone B1 ←→ Phone B2

Bridge link (manual):
    Phone A3 --[peer]--> Phone B1

Result:
    A1 ←→ A2 ←→ A3 ←→ B1 ←→ B2
         (4 nodes auto-mesh)  (2 nodes auto-mesh)
         └──────────┬───────────┘
              Bridge link
```

**Setup**:

```bash
# Subnet A (auto-discovery)
./wifi-chat -port 8000 -node node-A1
./wifi-chat -port 8000 -node node-A2
./wifi-chat -port 8000 -node node-A3 -peer ws://10.0.0.100:8000/ws/mesh

# Subnet B (auto-discovery)
./wifi-chat -port 8000 -node node-B1 -peer ws://192.168.1.12:8000/ws/mesh
./wifi-chat -port 8000 -node node-B2
```

**Result**:

- A1, A2, A3 auto-discover within Subnet A
- B1, B2 auto-discover within Subnet B
- A3 and B1 manually connect
- Full mesh spans both subnets: A1 ↔ A2 ↔ A3 ↔ B1 ↔ B2

---

### Scenario 6: Redundant Bridges (High Availability)

For resilience, create multiple peer connections between networks:

```
Subnet A                         Subnet B
  A1 ←→ A2 ←→ A3                B1 ←→ B2 ←→ B3
                 ╱╲              ╱╲
              bridge            bridge
                 ╲╱              ╲╱
                  │               │
            [DUAL LINK]
                 ╱╲              ╱╲
                A3→B1, B3→A3

If A3↔B1 fails, still have A3↔B3 backup
```

**Setup**:

```bash
# Subnet A
./wifi-chat -port 8000 -node node-A3 \
  -peer ws://10.0.0.100:8000/ws/mesh,ws://10.0.0.102:8000/ws/mesh

# Subnet B
./wifi-chat -port 8000 -node node-B1 -peer ws://192.168.1.12:8000/ws/mesh
./wifi-chat -port 8000 -node node-B3 \
  -peer ws://192.168.1.12:8000/ws/mesh
```

**Result**: Two redundant bridges ensure network continues even if one link fails.

---

## Part 5: Troubleshooting Cross-Network Multi-Hop

### Problem: Nodes Won't Connect

**Symptom**: Peer connection fails with errors like:

```
[MESH] Peer connect failed (ws://10.0.0.100:8000/ws/mesh): dial websocket: websocket: bad handshake
```

**Checklist**:

1. **IP Address is Correct?**

   ```bash
   # On Phone B, find its IP
   ifconfig  # macOS/Linux
   ipconfig  # Windows
   # Should see something like 10.0.0.100
   ```

2. **Port is Open?**

   ```bash
   # Try to reach the port from Phone A
   curl http://10.0.0.100:8000/api/health
   # Should return 200 OK
   ```

3. **Firewall allows WebSocket?**
   - Check if personal firewall on each phone blocks port 8000
   - On router, check if port forwarding is needed (usually not for LAN)
   - Some corporate WiFi blocks arbitrary ports

4. **Correct WebSocket URL?**
   - Must be: `ws://` (not `http://`)
   - Must include full path: `/ws/mesh`
   - Correct format: `ws://IP:PORT/ws/mesh`
   - ✓ Correct: `ws://10.0.0.100:8000/ws/mesh`
   - ✗ Wrong: `ws://10.0.0.100:8000` (missing `/ws/mesh`)
   - ✗ Wrong: `http://10.0.0.100:8000/ws/mesh` (should be `ws://`)

5. **Are nodes running?**
   ```bash
   # Check if server started
   # Look for: "WiFi Chat Server Started!"
   # And network URL: http://192.168.1.10:8000
   ```

---

### Problem: Nodes Connect But Users Don't Sync

**Symptom**:

- Peer connection shows `[MESH] Connected to peer: ws://...` ✓
- But users from other nodes don't appear in user list ✗

**Checklist**:

1. **Did you join on each node?**

   ```
   Phone A browser: http://192.168.1.10:8000
   → Enter username "alice"
   → Click "Join"

   Phone B browser: http://10.0.0.100:8000
   → Enter username "bob"
   → Click "Join"
   ```

   Users won't sync if you haven't clicked "Join" on each node.

2. **Wait for announce loop** (10 seconds)
   - After joining, wait 10+ seconds
   - Check server logs: `[MESH] Announce from ...`
   - Users are exchanged every 10 seconds

3. **Check logs for errors**

   ```
   Look for:
   [MESH] Announce from node-B: users=[bob]
   [MESH] Connected to peer: ws://10.0.0.100:8000/ws/mesh
   [HUB] Updated remote users from peer node-B

   If you don't see these, diagnose peer connection first
   ```

4. **Refresh browser**
   - User list is shown after announce loop runs
   - Try refreshing browser after waiting 10+ seconds
   - F5 or Cmd+R

---

### Problem: Messages Don't Relay

**Symptom**:

- Users appear in all browsers ✓
- But messages to remote users disappear ✗
- No error in console

**Checklist**:

1. **Check TTL in message**

   ```
   Maximum 7 hops by default
   If your chain is longer than 7, message won't relay

   Solution: Increase MaxTTL in internal/mesh/mesh.go:
   const MaxTTL = 15  // ← change from 7 to 15
   ```

2. **Verify message destination**
   - Is the recipient's exact username spelled correctly?
   - Usernames are case-sensitive
   - ✓ Correct: "alice"
   - ✗ Wrong: "Alice" or "ALICE" (won't find them)

3. **Check if users are on same network**

   ```
   If recipient is "local" (same node):
   → Message delivers normally

   If recipient is "remote" (different node):
   → Must relay through peers
   → Check peer connection is stable
   ```

4. **Look for deduplication**

   ```
   Message sent, but immediately dropped?
   Might be caught by dedup (alreadySeen):

   [Rare] If same message resent before dedup expires (5min),
   it will be dropped.

   Solution: Each message gets unique ID automatically.
   This is not usually the problem.
   ```

5. **Check store-and-forward queue**

   ```
   If recipient is unreachable:
   → Message queued in outbox
   → Will be delivered when user comes online

   Check logs: [MESH] Queued message for offline user
   ```

---

### Problem: Messages Only Travel 1-2 Hops

**Symptom**:

- A can reach B (1 hop) ✓
- B can reach C (1 hop) ✓
- But A cannot reach C (2 hops) ✗

**Checklist**:

1. **TTL too low?**

   ```
   Default MaxTTL = 7 hops
   If chain is exactly 2 hops, should work

   Add logging to verify TTL:
   Edit internal/mesh/mesh.go
   Add: log.Printf("Message TTL: %d", msg.TTL)
   ```

2. **Split-horizon blocking?**

   ```
   Messages never relay back to ingress peer
   This is correct (prevents loops)

   But if B only has 1 peer (A):
   B receives from A → TTL > 0 → tries to forward
   But only peer is A (ingress) → no forward
   → Message stops at B

   Solution: B needs 2+ peers for multi-hop
   ```

3. **Peer connection unstable?**

   ```
   Logs show:
   [MESH] Peer connect failed ... retrying in 5s
   [MESH] Peer disconnected ... reconnecting

   → Peer connection is flaky
   → Try: ping between networks
   → Check: firewall, network congestion
   ```

---

### Problem: Connection Flaps (Reconnecting Repeatedly)

**Symptom**:

```
[MESH] Connected to peer: ws://10.0.0.100:8000/ws/mesh
[MESH] Peer disconnected (ws://10.0.0.100:8000/ws/mesh) — reconnecting in 5s
[MESH] Peer connect failed (ws://10.0.0.100:8000/ws/mesh): ...
[MESH] Peer disconnected ... reconnecting in 5s
(repeats every 5 seconds)
```

**Causes**:

1. **Network path is unstable**
   - WiFi signal weak
   - Packet loss high
   - Too much congestion

   Solution:
   - Move closer to router
   - Check WiFi signal strength: `iwconfig` (Linux)
   - Try wired connection if possible

2. **Server crash on remote node**
   - Remote server keeps dying
   - Connection succeeds briefly, then peer closes

   Solution:
   - Check remote server logs
   - Restart remote server with error output

3. **Firewall/NAT timeout**
   - Router drops idle connections
   - After ~60s inactive, drops WebSocket

   Solution:
   - Increase ping frequency in code
   - Current ping: 45 seconds (in peer.writePump)
   - Should be good enough, but try shortening to 30s if it flaps

---

### Problem: Performance Degrades with More Hops

**Symptom**:

- A→B works great (low latency)
- A→B→C is slow
- A→B→C→D is very slow

**Expected Behavior** (Not a bug):

- Each relay node uses same WiFi channel
- Each message consumes airtime
- More hops = less throughput

```
Direct (1 hop):     ~100 Mbps
1 relay (2 hops):   ~50 Mbps (each node halves capacity)
2 relays (3 hops):  ~25 Mbps
3 relays (4 hops):  ~12 Mbps
```

**Mitigation**:

1. **Use separate WiFi channels** (if router supports)
   - Put different bridges on different 2.4GHz or 5GHz channels
   - More channels = less interference

2. **Reduce message frequency**
   - Don't send continuous messages
   - This is a fundamental WiFi property, not a bug

3. **Use wired backhaul** (if possible)
   - Connect some relay nodes via Ethernet
   - Frees up WiFi for data

---

### Debug: Check Mesh Status

**View connected peers** (from server logs):

```
Look for: [MESH] Connected to peer: ws://...
         [MESH] Peer disconnected: ...
         [MESH] Announce from ...
```

**View all users the mesh knows about**:

```
Server logs during announce:
[MESH] Announce from node-B (node=node-B): users=[bob, charlie] neighbors=[node-A, node-C]
```

**Enable verbose logging** (optional):
Edit `cmd/main.go` and `internal/mesh/mesh.go` to add more debug output:

```go
log.Printf("[MESH] Message TTL=%d, Hops=%d, HopPath=%v", msg.TTL, msg.Hops, msg.HopPath)
log.Printf("[MESH] Peer.readPump: destination=%s, forMe=%v", msg.To, isForMe)
```

---

### Checklist for Multi-Network Setup

Before debugging, verify:

- [ ] All nodes are running (`./wifi-chat` started on each)
- [ ] All nodes use same port (default 8000)
- [ ] Peer URLs are correct format: `ws://IP:PORT/ws/mesh`
- [ ] IP addresses are reachable: `ping IP`
- [ ] HTTP port accessible: `curl http://IP:8000/api/health`
- [ ] Users joined on each node (browser form submitted)
- [ ] Waited 10+ seconds for first announce
- [ ] Checked server logs for `[MESH]` and `[DISCOVERY]` messages
- [ ] No firewall blocking ports 8000 and 19540
- [ ] Chain topology is linear or star (not fully connected required)

---

## Part 6: Architecture Comparison

### Your Implementation vs WiFi Relay Concepts

From the WiFi relay standards guide:

| Mode                                   | Typical Range | Your Implementation                      |
| -------------------------------------- | ------------- | ---------------------------------------- |
| **Direct phone-to-phone WiFi (1 hop)** | ~30-100m      | Prerequisite (phones must connect first) |
| **WiFi Direct / Hotspot (1 hop)**      | ~30-100m      | Prerequisite (phones must connect first) |
| **Single relay (2 hops)**              | ~60-200m      | ✓ Fully supported (A→B→C)                |
| **Multi-hop mesh (3+ hops)**           | ~90-300m+     | ✓ **This is what you implement**         |

### Layer Mapping

| Layer                   | Standard                 | Your Code                                      |
| ----------------------- | ------------------------ | ---------------------------------------------- |
| **MAC Layer (Layer 2)** | 802.11s mesh             | ✗ Not implemented (requires WiFi radio access) |
| **IP Layer (Layer 3)**  | AODV, OLSR, B.A.T.M.A.N. | ✓ Your gossip protocol (simplified)            |
| **Application Layer**   | Custom protocols         | ✓ WebSocket + message types                    |

**Your approach**: Application-level, protocol-agnostic, works over any IP connection.

---

## Part 7: Known Limitations

### Throughput Degradation

Each hop consumes WiFi bandwidth. A 3-hop path has ~1/3 throughput of direct connection.

```
Direct (1 hop):     100 Mbps
1 relay (2 hops):   ~50 Mbps
2 relays (3 hops):  ~33 Mbps
3 relays (4 hops):  ~25 Mbps
```

### Latency Increase

Each hop adds ~10-50ms depending on network congestion.

```
Direct:             ~5ms RTT
1 relay:            ~15-30ms RTT
2 relays:           ~30-60ms RTT
```

### Battery Drain

Relay nodes must stay awake to forward traffic for others.

### No Per-Destination Routing

Mesh uses pure gossip (flood-and-forward) with TTL. Not optimal for sparse networks.

- Better approach: Routing tables + AODV/OLSR (future work)
- Current approach: Simple but works for dense LANs

### Security: Relay Nodes See Encrypted Traffic

Store-and-forward relies on E2EE being applied _before_ message reaches mesh.

- ✓ DMs are encrypted at browser before sending
- ✗ Relay nodes see plaintext if message isn't encrypted
- ✗ Relay nodes can read group chat messages (no E2EE for groups)

---

## Part 8: Missing UI Features

Your backend supports all mesh features, but **no frontend UI exists** for:

1. **Peer connection status** — Which peers are connected?
2. **Discovery debug info** — Which nodes are on the network?
3. **User source indicator** — Is this user local or remote? How many hops?
4. **Manual peer management** — Add/remove peers via UI (instead of CLI flags)
5. **Network topology diagram** — Visualize node connections
6. **Hop count display** — Show message relay path

---

## Part 9: Implementation Files

| File                              | Purpose                                    |
| --------------------------------- | ------------------------------------------ |
| `internal/mesh/mesh.go`           | Core mesh: peers, relay, dedup, forwarding |
| `internal/discovery/discovery.go` | UDP broadcast discovery + auto-connect     |
| `cmd/main.go`                     | CLI flags for manual peering               |
| `internal/server/server.go`       | Server setup, discovery integration        |
| `internal/hub/hub.go`             | User presence, mesh callbacks              |
| `internal/models/message.go`      | Message types, multi-hop fields            |

---

## Part 10: Summary

**Your mesh is**:

- ✓ Application-layer (Layer 3+) gossip protocol
- ✓ Multi-hop TTL-based relay
- ✓ Automatic discovery (UDP broadcast) + manual peering
- ✓ Zero-config for same-subnet use cases
- ✓ Cross-subnet capable (with manual peering)
- ✓ Store-and-forward for offline users
- ✓ Compatible with Android MeshRouter

**Ideal for**:

- Local area networks (WiFi roaming, offline events)
- Disaster/emergency scenarios (no internet)
- Mesh networks where nodes are dynamic
- Private, decentralized communication

**Trade-offs vs. WiFi-layer mesh (802.11s)**:

- ✓ Simpler (no WiFi radio control needed)
- ✓ More portable (works over any IP)
- ✗ Lower throughput
- ✗ Higher latency
