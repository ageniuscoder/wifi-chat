# RFC 6455: WebSocket Protocol Notes

RFC 6455 defines the WebSocket protocol, a full-duplex, message-oriented
transport over a single TCP connection. The protocol is meant for browser
applications that need low-latency bidirectional communication without
maintaining a large number of HTTP requests or using polling techniques.

WebSocket is not a replacement for HTTP itself. The protocol starts with an
HTTP request/response exchange, and then upgrades the same TCP connection to
a WebSocket stream. After that, the connection carries WebSocket frames.

## 1. Handshake: from HTTP to WebSocket

A WebSocket session begins with the client performing an HTTP Upgrade request.
That request must include the following ideas:

- `GET` request targeting a WebSocket endpoint
- `Upgrade: websocket` header
- `Connection: Upgrade` header
- `Sec-WebSocket-Key` header with a value chosen by the client
- optional `Sec-WebSocket-Protocol` and `Sec-WebSocket-Extensions` headers

The server validates these headers and, if accepted, replies with:

- `HTTP 101 Switching Protocols`
- `Upgrade: websocket`
- `Connection: Upgrade`
- `Sec-WebSocket-Accept` header computed from the key

The RFC uses a specific calculation:

- The server concatenates the `Sec-WebSocket-Key` with the fixed GUID
  `258EAFA5-E914-47DA-95CA-C5AB0DC85B11`
- The result is hashed with SHA-1
- The digest is base64-encoded
- That value is placed in `Sec-WebSocket-Accept`

This proves the server understood the handshake and is willing to run the
WebSocket protocol over the same socket.

## 2. Why the connection is long-lived

Once the handshake succeeds, the HTTP connection is upgraded to a transport
that can carry multiple messages in both directions. The important RFC model
is:

- one TCP connection
- full-duplex communication
- logical messages framed over the stream
- persistent state across message boundaries

This is why a WebSocket is called a long-lived connection. The connection is
not created for a single request and then abandoned after one response.
Instead, it remains open for the whole session and is reused as the app sends
and receives messages.

That is also why a WebSocket server usually maintains two concurrent
lifecycles:

- a read loop that consumes incoming frames
- a write loop that emits outgoing frames

In a Go implementation, those maps to operations such as `ReadMessage()` and
`WriteMessage()`.

## 3. Frame format

After upgrade, all payloads are carried in WebSocket frames. A frame is a
small binary structure layered onto TCP. The RFC defines the layout of the
header and the payload.

A frame header begins with 2 bytes:

- `FIN` bit: indicates whether this is the final fragment of a message
- `RSV1`, `RSV2`, `RSV3`: reserved flags used for extensions
- `opcode`: type of the frame
- `MASK` bit: indicates whether the payload is masked
- payload length field: 7 bits, 16 bits, or 64 bits depending on size

The optional extended payload length is used when the length is larger than
125 bytes. If the payload length is 126, the next 2 bytes hold the actual
length. If it is 127, the next 8 bytes hold the actual length.

If the `MASK` bit is set, a 4-byte masking key follows. This key is used to
unmask the payload data. The masking rule is important:

- client-to-server frames must be masked
- server-to-client frames must not be masked

That makes the protocol safe against a certain class of attacks from
untrusted browser clients.

## 4. Message fragmentation

A WebSocket application message is not necessarily one physical frame. The
RFC allows a logical message to be fragmented across multiple frames.

The receiver must keep a small parser state:

- accumulate a sequence of fragments
- inspect the `FIN` bit of each frame
- if `FIN` is `1`, the current fragment ends the logical message

The application sees one logical message only once the TCP stream has been
reassembled into a message boundary. This matters because a client or server
can send arbitrarily large messages, and the RFC expects frames to be handled
incrementally rather than assuming "one frame equals one full message".

## 5. Opcodes

The opcode field tells the receiver what kind of payload is carried:

- `0x1` — text frame
- `0x2` — binary frame
- `0x8` — close frame
- `0x9` — ping frame
- `0xA` — pong frame

A text frame carries UTF-8 text data. A binary frame carries arbitrary byte
payloads. A close frame contains a two-byte status code and optional close
reason. The ping and pong frames are control frames and are used to check
liveness of both sides.

The control frames (`ping`, `pong`, `close`) must be handled quickly and may
interleave with normal data frames. They are not normal application messages,
but they are part of the protocol loop.

## 6. Control frames and liveness

RFC 6455 defines ping and pong frames so endpoints can detect broken
connections without waiting for the TCP stack to detect failure.

- A ping frame asks the peer to respond with a pong
- A pong frame is the peer’s response
- A peer that fails to reply can be considered dead or slow

This is why WebSocket implementations often set read deadlines or use a
ticker-based ping loop. In this codebase, the write pump uses a ping ticker
and `WriteMessage(websocket.PingMessage, nil)` to send control traffic.

The close frame is used for graceful shutdown:

- either endpoint can send a close frame
- the peer may respond with its own close frame
- the TCP connection can remain open long enough to carry the close
  handshake and then be shut down

The code uses `WriteMessage(websocket.CloseMessage, []byte{})` inside the
write loop when the outgoing send channel is closed.

## 7. Error and disconnect handling

The RFC expects endpoints to read frames continuously and treat protocol
violations as close-worthy events. A connection is considered unusable if a
peer:

- sends malformed frame structure
- sends incorrect masking behavior
- fails to conform to expected UTF-8 or close semantics
- stops responding to expected control traffic

In the Go client code, the receive side does `ReadMessage()` in a loop. When
that call returns an error, `ReadPump()` exits and triggers teardown. That
captures the RFC idea that WebSocket reliability depends on the connection
being able to continue cleanly or close cleanly when it cannot.

## 8. Application-level meaning

Even though the RFC defines the wire-level rules, it does not define chat
application semantics. The WebSocket layer is just a transport. The actual
application payload is application-defined:

- JSON text frames for chat input/output
- binary frames for file transfer or encryption data
- control frames for connection-management signals

That is why a package like `gorilla/websocket` exposes the lower-level wire
operations while the application chooses its own message format.

## 9. Server-side lifecycle in this project

In this repository, the server accepts an upgraded WebSocket connection and
creates a `Client` object. Two goroutines are started:

- `ReadPump()` reads inbound frames and sends them to the hub
- `WritePump()` drains outbound frame bytes from `c.Send` and writes them
  to the socket

The lifecycle is intentionally separated:

- `ReadPump` handles inbound flow and detects disconnects/errors
- `WritePump` handles outbound flow and ping frames

When a goroutine ends, its `defer` executes and performs cleanup:

- `Hub.Unregister` removes the client from hub ownership/state
- `Conn.Close()` shuts down the socket

This is correct for WebSocket because they are established once and then kept
alive for many messages. The close is a shutdown operation for that session,
not a normal operation that happens for every message.
