package store

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// StoredMessage represents a persisted chat message
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
	MsgID     string    `json:"msg_id,omitempty"`
	Status    string    `json:"status,omitempty"`
}

// MessageStore persists messages to a JSONL file and keeps them in memory
type MessageStore struct {
	messages []StoredMessage
	nextID   int64
	filePath string
	file     *os.File
	mu       sync.RWMutex
}

// NewMessageStore creates a persistent message store backed by a JSONL file
func NewMessageStore(dataDir string) *MessageStore {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory %s: %v", dataDir, err)
	}

	fp := filepath.Join(dataDir, "messages.jsonl")
	s := &MessageStore{
		messages: make([]StoredMessage, 0, 1000),
		nextID:   1,
		filePath: fp,
	}

	s.loadFromDisk()

	f, err := os.OpenFile(fp, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Failed to open message file %s: %v", fp, err)
	}
	s.file = f

	log.Printf("Message store loaded %d messages from %s", len(s.messages), fp)
	return s
}

// loadFromDisk reads existing messages from the JSONL file
func (s *MessageStore) loadFromDisk() {
	f, err := os.Open(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		log.Printf("Warning: could not read message file: %v", err)
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var msg StoredMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			log.Printf("Warning: skipping corrupt message line: %v", err)
			continue
		}
		s.messages = append(s.messages, msg)
		if msg.ID >= s.nextID {
			s.nextID = msg.ID + 1
		}
	}
}

// appendToDisk writes a single message to the JSONL file
func (s *MessageStore) appendToDisk(msg StoredMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error marshaling message for disk: %v", err)
		return
	}
	data = append(data, '\n')
	if _, err := s.file.Write(data); err != nil {
		log.Printf("Error writing message to disk: %v", err)
	}
}

// Close closes the backing file
func (s *MessageStore) Close() {
	if s.file != nil {
		s.file.Close()
	}
}

// MaxInMemoryMessages limits in-memory message count to prevent unbounded growth
const MaxInMemoryMessages = 50000

// SaveMessage saves a room message
func (s *MessageStore) SaveMessage(msg StoredMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	msg.ID = s.nextID
	s.nextID++
	msg.Timestamp = time.Now()
	msg.IsDM = false
	s.messages = append(s.messages, msg)
	s.evictOldLocked()
	s.appendToDisk(msg)
}

// SaveDM saves a direct message
func (s *MessageStore) SaveDM(msg StoredMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	msg.ID = s.nextID
	s.nextID++
	msg.Timestamp = time.Now()
	msg.IsDM = true
	s.messages = append(s.messages, msg)
	s.evictOldLocked()
	s.appendToDisk(msg)
}

// evictOldLocked removes oldest messages when in-memory count exceeds cap (caller must hold lock)
func (s *MessageStore) evictOldLocked() {
	if len(s.messages) > MaxInMemoryMessages {
		excess := len(s.messages) - MaxInMemoryMessages
		s.messages = s.messages[excess:]
	}
}

// GetRoomMessages returns the last N messages for a room
func (s *MessageStore) GetRoomMessages(room string, limit int) []StoredMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []StoredMessage
	for _, m := range s.messages {
		if !m.IsDM && m.Room == room {
			result = append(result, m)
		}
	}

	if len(result) > limit {
		result = result[len(result)-limit:]
	}
	return result
}

// UpdateDMStatus updates the status of a stored DM by its MsgID and persists to disk
func (s *MessageStore) UpdateDMStatus(msgID string, status string) bool {
	if msgID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := len(s.messages) - 1; i >= 0; i-- {
		if s.messages[i].IsDM && s.messages[i].MsgID == msgID {
			if s.messages[i].Status == status {
				return true // no change needed
			}
			s.messages[i].Status = status
			s.rewriteDiskLocked()
			return true
		}
	}
	return false
}

// rewriteDiskLocked rewrites the entire JSONL file from in-memory state.
// Caller must hold s.mu write lock.
func (s *MessageStore) rewriteDiskLocked() {
	if s.file != nil {
		s.file.Close()
	}
	// Write to a temp file first, then rename for atomicity
	f, err := os.Create(s.filePath)
	if err != nil {
		log.Printf("Error rewriting message file: %v", err)
		return
	}
	w := bufio.NewWriter(f)
	for _, msg := range s.messages {
		data, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		w.Write(data)
		w.WriteByte('\n')
	}
	w.Flush()
	f.Close()
	// Reopen in append mode for subsequent writes
	af, err := os.OpenFile(s.filePath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Error reopening message file after rewrite: %v", err)
		return
	}
	s.file = af
}

// GetDMs returns DMs between two users
func (s *MessageStore) GetDMs(user1, user2 string, limit int) []StoredMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []StoredMessage
	for _, m := range s.messages {
		if m.IsDM && ((m.From == user1 && m.To == user2) || (m.From == user2 && m.To == user1)) {
			result = append(result, m)
		}
	}

	if len(result) > limit {
		result = result[len(result)-limit:]
	}
	return result
}
