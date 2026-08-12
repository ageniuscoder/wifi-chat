# About the Hub Pattern

The Hub pattern is the standard Go approach for managing many concurrent WebSocket clients.

Instead of letting each connection hold and mutate shared state on its own, the application centralizes client state and message routing inside a single runtime coordinator: the `Hub`.

This design is useful because it:

- keeps message distribution in one place
- avoids cross-client race conditions
- reduces locking overhead
- makes client registration and teardown predictable

A chat server usually consists of two major building blocks:

- `Client`: one WebSocket connection for one user
- `Hub`: the event loop that knows who is connected and what should be broadcast

The important idea is that the browser connection does not talk to the database or room list directly. It sends messages through the hub, and the hub decides what to do with them.

## A Minimal Hub Shape

A simplified hub looks like this:

```go
type Hub struct {
    clients map[*Client]bool

    broadcast chan []byte
    register chan *Client
    unregister chan *Client
}

func NewHub() *Hub {
    return &Hub{
        broadcast:   make(chan []byte),
        register:    make(chan *Client),
        unregister:  make(chan *Client),
        clients:     make(map[*Client]bool),
    }
}
```

That structure represents the three major data flows in the system:

- clients joining the hub
- clients leaving the hub
- messages being routed out to connected clients

## The Single Event Loop

The `Hub` runs a dedicated goroutine that processes all channel traffic through a `select` loop:

```go
func (h *Hub) Run() {
    for {
        select {
        case client := <-h.register:
            h.clients[client] = true

        case client := <-h.unregister:
            if _, ok := h.clients[client]; ok {
                delete(h.clients, client)
                close(client.send)
            }

        case message := <-h.broadcast:
            for client := range h.clients {
                select {
                case client.send <- message:
                default:
                    close(client.send)
                    delete(h.clients, client)
                }
            }
        }
    }
}
```

This is the core reason the pattern is safe and scalable.

The `Hub` is the only component that mutates the `clients` table, so it can do that work in one goroutine and avoid races that would appear if each client tried to write the same shared data structure.

## The Client Struct

Each user connection is represented with a `Client` object:

```go
type Client struct {
    hub *Hub
    conn *websocket.Conn
    send chan []byte
}
```

The client has three key responsibilities:

- hold the websocket connection
- hold a buffered outbound channel
- expose a back-reference to the hub so it can register/unregister and route messages

## Read/Write Pumps

When a user connects, the HTTP upgrade step creates a websocket connection and registers the client with the hub.

The client then gets two goroutines:

- `readPump()` reads from the socket and forwards incoming bytes into the hub
- `writePump()` reads from the client's buffered `send` channel and writes them back to the browser

A simplified version looks like this:

```go
func (c *Client) readPump() {
    defer func() {
        c.hub.unregister <- c
        c.conn.Close()
    }()

    for {
        _, message, err := c.conn.ReadMessage()
        if err != nil {
            break
        }
        c.hub.broadcast <- message
    }
}

func (c *Client) writePump() {
    defer c.conn.Close()

    for {
        select {
        case message, ok := <-c.send:
            if !ok {
                c.conn.WriteMessage(websocket.CloseMessage, []byte{})
                return
            }
            c.conn.WriteMessage(websocket.TextMessage, message)
        }
    }
}
```

## Bootstrapping the Pattern

The HTTP handler upgrades the request to a websocket connection:

```go
func serveWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Println(err)
        return
    }

    client := &Client{hub: hub, conn: conn, send: make(chan []byte, 256)}

    client.hub.register <- client

    go client.writePump()
    go client.readPump()
}
```

That pattern is the basic lifecycle for any hub-driven websocket service.

## Why This Pattern Works

### Channels over locks

The hub runs one event loop. That means it owns the update path for the connected-client registry instead of each goroutine writing directly to a shared map.

### Backpressure handling

Each client has a buffered send channel. If a client is too slow or their internet is unstable, the buffer can fill up. The hub can fall back to `default` logic and drop or disconnect that client instead of blocking the entire runtime.

### Separation of concerns

- The hub decides what a message means and where it should flow.
- The client only knows how to read raw websocket data and write outbound bytes.

This separation keeps the architecture understandable and easy to extend as the app grows.
