# WiFi Relay and Multi-Hop Communication

**Reference**: [How Wireless Communication Actually Works - Engineering Explained](https://evelta.com/blog/how-wireless-communication-actually-works-engineering-explained/)

---

## Overview

WiFi relay and multi-hop WiFi relay enable two phones to communicate even when they are **not directly in radio range**. The basic concept is simple:

> **A relay node receives a WiFi packet/frame and retransmits it toward the destination.**

---

## Part 1: Direct Phone-to-Phone WiFi (One Hop)

Two phones can communicate directly over WiFi in several common ways:

### A. Through the Same WiFi Access Point

```
Phone A --WiFi--> Router/AP <--WiFi-- Phone B
```

- Both phones connect to the same router/AP
- Phone A sends data to the router
- Router forwards it to Phone B

**Note**: This is technically a relay, but it's only **one infrastructure hop**, not a phone-to-phone relay chain.

---

### B. WiFi Direct

WiFi Direct is the standard way two phones connect without an external router.

**How it works:**

1. Both phones discover each other using WiFi probe requests/responses
2. One phone becomes the **Group Owner (GO)** — acts like a mini access point
3. The other phone connects to the GO like a normal WiFi client
4. The GO may run a DHCP server and assign an IP address
5. Both phones can now exchange data directly

**Example:**
```
Phone A (Group Owner) <--WiFi Direct--> Phone B (Client)
```

**Important**: This is still **one hop**. The two phones must be within radio range of each other.

---

### C. Hotspot / Tethering

One phone creates a WiFi hotspot. The other phone connects to it like a normal router.

**This is also one hop.**

---

## Part 2: Multi-Hop Communication (Out of Range)

When phones are too far apart to hear each other directly, intermediate relay nodes are needed.

### Example Scenario

```
A --WiFi--> B --WiFi--> C --WiFi--> D
            (relay)     (relay)
```

**The problem:**
- A and D cannot communicate directly
- B and C are relay nodes in between
- A sends data to B → B forwards to C → C forwards to D

**Result**: Multi-hop mesh enables communication across distances that would otherwise be impossible.

---

## Part 3: How Relay Nodes Forward Data

A relay node must know two things:

1. **Who is the final destination?**
2. **Which neighbor should receive the packet next?**

This is handled by a **routing table**.

### Example Routing Table (On Node B)

| Destination | Next Hop |
|-------------|----------|
| A           | A        |
| C           | C        |
| D           | C        |

When B receives a packet destined for D, it looks up the table and finds: "next hop = C", then forwards the packet to C.

---

### Forwarding Layers

Forwarding can happen at **two main layers**:

#### A. Layer 2 Mesh — 802.11s (MAC-Layer Forwarding)

- WiFi frames are forwarded like in a network switch
- Mesh network uses a mesh header with source/destination addresses
- Each mesh node forwards frame hop-by-hop
- Standard: **IEEE 802.11s**
- **Limitation**: Most smartphones do NOT implement 802.11s in normal WiFi

#### B. Layer 3 Routing — IP Forwarding

- Each phone has an IP address
- Relay phone acts like a small router
- Receives IP packet → checks destination IP → finds next hop → forwards packet
- WiFi MAC address **changes at each hop**, but source/destination IPs stay the same
- TTL field in IP header is **decremented at each hop** to prevent loops

**Common routing protocols** used in phone-based mesh apps:
- OLSR (Optimized Link State Routing)
- AODV (Ad Hoc On-Demand Distance Vector)
- B.A.T.M.A.N. (Better Approach To Mobile Ad-hoc Networking)
- HWMP (Hybrid Wireless Mesh Protocol)

---

## Part 4: How Multi-Hop Paths Are Created

For a multi-hop network to work, phones must perform three key functions:

### 1. Discover Neighbors

Each node sends beacon or "hello" messages to find nearby nodes.

### 2. Build a Routing Table

A routing protocol chooses the best path to each destination based on metrics:
- **Number of hops** (prefer shorter paths)
- **Signal strength / link quality** (prefer stronger links)
- **Battery level** (prefer nodes with more power)

### 3. Forward Data Hop-by-Hop

When a packet arrives, the relay node checks:
- **Is this packet for me?** → Yes: deliver to app
- **Is this packet for me?** → No: look up next hop and retransmit

### Data Flow Example

```
Scenario: A wants to send to D

Step 1 (A → B):
  Frame: from=A, to=D
  B receives message

Step 2 (B's decision):
  Check: destination is D, not me
  Look up routing table: D's next hop is C
  Forward to C

Step 3 (B → C):
  Frame: from=B, to=D
  C receives message

Step 4 (C's decision):
  Check: destination is D, not me
  Look up routing table: D is directly reachable
  Forward to D

Step 5 (C → D):
  Frame: from=C, to=D

Step 6 (D receives):
  Check: destination is D, YES that's me!
  Deliver to app
```

---

## Part 5: WiFi Direct and Multi-Hop on Real Phones

Standard WiFi Direct is **NOT** designed for multi-hop. It typically forms a single group with one Group Owner and one or more clients.

### The Challenge

For a true multi-hop chain:

```
A -- GO/Client --> B -- GO/Client --> C -- GO/Client --> D
```

The relay phone **B** must be part of **two WiFi Direct groups simultaneously**:
- One group with A
- Another group with C

**Requirements:**
- Multi-role concurrent WiFi support, OR
- Multiple WiFi radios/interfaces

**Problem**: Not all phones support this!

### Real-World Solutions

Many mesh apps combine multiple technologies:
- WiFi Direct for some links
- Bluetooth for other links
- Normal WiFi AP/client mode

**Examples**: Briar, Bridgefy, FireChat and similar offline mesh apps often use Bluetooth or mixed radios because **true WiFi multi-hop is hard on normal phones**.

---

## Part 6: Important Limitations

### ⚠️ Throughput Drops with Each Hop

If all nodes use the same WiFi channel, every retransmission uses the same radio time.

```
Direct (1 hop):     ~100 Mbps
1 relay (2 hops):   ~50 Mbps (halved)
2 relays (3 hops):  ~33 Mbps
3 relays (4 hops):  ~25 Mbps
```

A two-hop path can **cut throughput by more than half**.

### ⚠️ Hidden Node Problem

In WiFi, two nodes may not hear each other but both try to send to the same relay → **causes collisions**.

### ⚠️ Latency Increases

Each hop adds delay (typically 10-50ms per hop).

### ⚠️ Battery Drain

Relay phones must stay awake and retransmit data for others → significant power consumption.

### ⚠️ Security Risk

A relay node can see the traffic **unless data is encrypted end-to-end by the application**.

---

## Part 7: Simple Analogy

Think of a **chain of people passing a message**:

```
1. A wants to send a message to D, but D is too far away
2. A tells B the message
3. B hears the message and repeats it to C
4. C repeats it to D
5. D receives the message
```

Each person doesn't need to understand the message. They only need to know:
- **Who is the final destination?**
- **Who is the next person in the chain?**

**That is exactly what a WiFi relay or mesh node does.**

---

## Summary: Communication Modes Comparison

| Mode | Hops | Typical Range | Use Case |
|------|------|---------------|----------|
| **Normal WiFi via AP** | 2 | ~30-100m | Both phones on same router |
| **WiFi Direct / Hotspot** | 1 | ~30-100m | Direct phone-to-phone |
| **Single Relay** | 2 | ~60-200m | One phone forwards between two others |
| **Multi-Hop Mesh** | 3+ | ~90-300m+ | Chain of phones relays data |

---

## Key Takeaway

When two phones are **out of range**, they use **intermediate relay nodes**. Each relay receives a packet, checks the destination, and forwards it to the next hop. With a routing protocol, the network can **automatically find a path** through several phones, creating a **multi-hop WiFi mesh** that extends communication beyond normal radio range.

---

*This content is for reference and educational purposes. For WiFi Chat implementation details, see [mesh-and-discovery.md](mesh-and-discovery.md).*
