package models

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"
)

// MessageType represents the type of WebSocket message
type MessageType string

const (
	// Client -> Server
	MsgTypeJoin       MessageType = "join"
	MsgTypeMessage    MessageType = "message"
	MsgTypeDM         MessageType = "dm"
	MsgTypeImage      MessageType = "image"
	MsgTypeCreateRoom MessageType = "create_room"
	MsgTypeJoinRoom   MessageType = "join_room"
	MsgTypeLeaveRoom  MessageType = "leave_room"
	MsgTypeTyping     MessageType = "typing"
	MsgTypeListRooms  MessageType = "list_rooms"
	MsgTypeListUsers  MessageType = "list_users"

	// Call signaling (peer-to-peer via server relay)
	MsgTypeCallRequest MessageType = "call_request"
	MsgTypeCallAccept  MessageType = "call_accept"
	MsgTypeCallReject  MessageType = "call_reject"
	MsgTypeCallOffer   MessageType = "call_offer"
	MsgTypeCallAnswer  MessageType = "call_answer"
	MsgTypeCallICE     MessageType = "call_ice"
	MsgTypeCallEnd     MessageType = "call_end"

	// Key exchange (E2EE)
	MsgTypeKeyExchange MessageType = "key_exchange"

	// DM History
	MsgTypeGetDMHistory MessageType = "get_dm_history"
	MsgTypeDMHistory    MessageType = "dm_history"

	// Mesh (node-to-node)
	MsgTypeMeshSync MessageType = "mesh_sync"

	// Distributed mesh (Android MeshRouter interop)
	MsgTypeAnnounce      MessageType = "announce"
	MsgTypeDeliveryAck   MessageType = "delivery_ack"
	MsgTypeReadReceipt   MessageType = "read_receipt"
	MsgTypePeerDiscovery MessageType = "peer_discovery"
	MsgTypeStoreForward  MessageType = "store_forward"

	// Server -> Client
	MsgTypeUserJoined MessageType = "user_joined"
	MsgTypeUserLeft   MessageType = "user_left"
	MsgTypeRoomList   MessageType = "room_list"
	MsgTypeUserList   MessageType = "user_list"
	MsgTypeHistory    MessageType = "history"
	MsgTypeError      MessageType = "error"
	MsgTypeSystem     MessageType = "system"
	MsgTypeRoomUsers  MessageType = "room_users"
)

// Message is the JSON envelope for all WebSocket communication
type Message struct {
	Type       MessageType       `json:"type"` //struct tag
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
	Status string `json:"status,omitempty"` // "delivered", "read"
	RefID  string `json:"ref_id,omitempty"` // references original message_id

	// Peer topology (announce messages)
	Neighbors []string `json:"neighbors,omitempty"`

	// Store-and-forward
	QueuedFor string `json:"queued_for,omitempty"`
}

// ChatMessage is a stored message record
type ChatMessage struct {
	ID        int64     `json:"id"`
	Room      string    `json:"room"`
	From      string    `json:"from"`
	To        string    `json:"to,omitempty"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"ts"`
	IsDM      bool      `json:"is_dm"`
	MsgID     string    `json:"msg_id,omitempty"`
	Status    string    `json:"status,omitempty"` // "sent", "delivered", "read"
}

// NewTimestamp returns current unix millis
func NewTimestamp() int64 {
	return time.Now().UnixMilli()
}

// GenerateMsgID returns a unique message ID for mesh deduplication
func GenerateMsgID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%d-%s", time.Now().UnixNano(), hex.EncodeToString(b))
}

var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-\.]{1,32}$`)

// ValidateUsername checks if a username is valid
func ValidateUsername(username string) bool {
	return usernameRegex.MatchString(username)
}

// TruncateContent limits content to MaxContentLength (measured in runes, not bytes,
// to avoid splitting multi-byte UTF-8 characters like emoji)
func TruncateContent(s string, MaxContentLength int) string {
	runes := []rune(s)
	if len(runes) > MaxContentLength {
		return string(runes[:MaxContentLength])
	}
	return s
}
