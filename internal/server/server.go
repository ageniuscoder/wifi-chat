package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"wifi-chat/internal/discovery"
	"wifi-chat/internal/hub"
	"wifi-chat/internal/mesh"
	"wifi-chat/internal/models"
	"wifi-chat/internal/store"

	"github.com/gorilla/websocket"
)

var allowedImageTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for LAN use
	},
}

// server config
type serverConfig struct {
	UploadDir          string
	MaxUploadSize      int64
	LocalMsgPersistDir string
	MaxContentLen      int64
	Port               int64
}

func LoadConfig() *serverConfig {
	cfg := &serverConfig{
		UploadDir:          "./uploads",
		MaxUploadSize:      10 << 20,
		LocalMsgPersistDir: "./data",
		MaxContentLen:      10000,
		Port:               8000,
	}

	if strPort := os.Getenv("PORT"); strPort != "" {
		if intPort, err := strconv.Atoi(strPort); err == nil {
			cfg.Port = int64(intPort)
		}
	}
	if uploadDir := os.Getenv("UPLOAD_DIR"); uploadDir != "" {
		cfg.UploadDir = uploadDir
	}
	if localMsgPersistDir := os.Getenv("LOCAL_MSG_PERSIST_DIR"); localMsgPersistDir != "" {
		cfg.LocalMsgPersistDir = localMsgPersistDir
	}
	if v := os.Getenv("MAX_UPLOAD_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxUploadSize = int64(n)
		}
	}
	if v := os.Getenv("MAX_CONTENT_LENGTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxContentLen = int64(n)
		}
	}

	return cfg
}

// Server holds the HTTP server and chat hub
type Server struct {
	Hub        *hub.Hub
	Store      *store.MessageStore
	Mesh       *mesh.Mesh
	Discovery  *discovery.Discovery
	Port       int
	HTTPServer *http.Server
	Config     *serverConfig
}

// NewServer creates a new server
func NewServer(port int, nodeName string, cfg *serverConfig) *Server {
	msgStore := store.NewMessageStore(cfg.LocalMsgPersistDir)
	h := hub.NewHub(msgStore, cfg.MaxContentLen)
	m := mesh.New(nodeName)
	h.SetMesh(m)

	// Auto-discovery for mesh peers
	disc := discovery.New(nodeName, port)
	disc.OnPeerFound = func(nodeID string, addr string) {
		if !m.IsConnectedTo(addr) {
			log.Printf("[DISCOVERY] Auto-connecting to peer: %s at %s", nodeID, addr)
			m.ConnectPeer(addr)
		}
	}

	return &Server{
		Hub:       h,
		Store:     msgStore,
		Mesh:      m,
		Discovery: disc,
		Port:      port,
		Config:    cfg,
	}
}

// ConnectPeer connects to an upstream mesh peer
func (s *Server) ConnectPeer(url string) {
	s.Mesh.ConnectPeer(url)
}

// Start starts the HTTP server and WebSocket hub
func (s *Server) Start() error {
	go s.Hub.Run()
	s.Discovery.Start()

	// Ensure uploads directory exists
	if err := os.MkdirAll(s.Config.UploadDir, 0755); err != nil {
		return fmt.Errorf("failed to create uploads directory: %w", err)
	}

	mux := http.NewServeMux()

	// Serve uploaded images
	mux.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(s.Config.UploadDir))))

	// Image upload endpoint
	mux.HandleFunc("/api/upload", s.handleUpload)

	// Health check
	mux.HandleFunc("/api/health", s.handleHealthCheck)

	// WebSocket endpoint
	mux.HandleFunc("/ws", s.handleWebSocket)

	// Mesh peering endpoint
	mux.HandleFunc("/ws/mesh", s.handleMeshPeer)

	// Serve static frontend files (must be last — catches all routes)
	fs := http.FileServer(http.Dir("./frontend"))
	mux.Handle("/", fs)

	addr := fmt.Sprintf("0.0.0.0:%d", s.Port)

	s.HTTPServer = &http.Server{
		Addr:         addr,
		Handler:      s.loggingMiddleware(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	s.printBanner()

	log.Printf("Server listening on %s", addr)
	return s.HTTPServer.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown() {
	s.Discovery.Stop()
	s.Mesh.Stop()
	s.Store.Close()
}

// loggingMiddleware logs HTTP requests
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		// Only log API/WS requests, not static file requests
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws") {
			log.Printf("%s %s %s %v", r.RemoteAddr, r.Method, r.URL.Path, time.Since(start))
		}
	})
}

func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	client := hub.NewClient(s.Hub, conn)
	s.Hub.Register <- client

	go client.WritePump()
	go client.ReadPump()
}

func (s *Server) handleMeshPeer(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Mesh WebSocket upgrade error: %v", err)
		return
	}

	// Read initial hello to get peer ID
	peerID := r.RemoteAddr
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, data, err := conn.ReadMessage()
	if err == nil {
		var hello models.Message
		if json.Unmarshal(data, &hello) == nil && hello.From != "" {
			peerID = hello.From
		}
	}
	conn.SetReadDeadline(time.Time{}) // clear deadline

	log.Printf("[MESH] Incoming peer connection from: %s", peerID)
	s.Mesh.AcceptPeer(conn, peerID) // blocks until peer disconnects
}

// handleUpload handles image file uploads via multipart form
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.Config.MaxUploadSize)
	if err := r.ParseMultipartForm(s.Config.MaxUploadSize); err != nil {
		http.Error(w, "File too large (max 10MB)", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "No image file provided", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Detect content type from file header
	buf := make([]byte, 512)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		http.Error(w, "Failed to read file", http.StatusInternalServerError)
		return
	}
	contentType := http.DetectContentType(buf[:n])

	ext, ok := allowedImageTypes[contentType]
	if !ok {
		http.Error(w, "Invalid file type. Allowed: JPEG, PNG, GIF, WebP", http.StatusBadRequest)
		return
	}

	// Reset file read position
	file.Seek(0, io.SeekStart)

	// Generate unique filename
	randBytes := make([]byte, 16)
	rand.Read(randBytes)
	filename := hex.EncodeToString(randBytes) + ext

	dst, err := os.Create(filepath.Join(s.Config.UploadDir, filename))
	if err != nil {
		log.Printf("Error creating upload file: %v", err)
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		log.Printf("Error writing upload file: %v", err)
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}

	imageURL := "/uploads/" + filename
	log.Printf("Image uploaded: %s (%s, %d bytes)", filename, header.Filename, header.Size)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"url":      imageURL,
		"filename": header.Filename,
	})
}

// generateID creates a random hex ID
func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) printBanner() {
	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║             WiFi Chat Server Started!               ║")
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Printf("║  Local:   http://localhost:%d\n", s.Port)

	wslIP := getLocalIP()
	isWSL := isWSL2()

	if isWSL {
		fmt.Printf("║  WSL IP:  http://%s:%d  (internal only)\n", wslIP, s.Port)
		if hostIP := getWindowsHostIP(); hostIP != "" {
			fmt.Printf("║  Phone:   http://%s:%d\n", hostIP, s.Port)
		}
		fmt.Println("╠══════════════════════════════════════════════════════╣")
		fmt.Println("║  WSL2 detected! Run in Windows PowerShell (Admin):  ║")
		fmt.Printf("║  netsh interface portproxy add v4tov4 ^\n")
		fmt.Printf("║    listenport=%d listenaddress=0.0.0.0 ^\n", s.Port)
		fmt.Printf("║    connectport=%d connectaddress=%s\n", s.Port, wslIP)
	} else if wslIP != "" {
		fmt.Printf("║  Network: http://%s:%d\n", wslIP, s.Port)
	}
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Println("║  Share the Network URL with people on your WiFi     ║")
	fmt.Println("╚══════════════════════════════════════════════════════╝")
}

// getLocalIP returns the non-loopback local IP of the host
func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				ip := ipnet.IP.String()
				// Skip Docker bridge IPs
				if strings.HasPrefix(ip, "172.17.") || strings.HasPrefix(ip, "172.18.") {
					continue
				}
				return ip
			}
		}
	}
	return ""
}

// isWSL2 checks if we are running inside WSL2
func isWSL2() bool {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(data))
	return strings.Contains(lower, "microsoft") || strings.Contains(lower, "wsl")
}

// getWindowsHostIP tries to get the Windows host IP from WSL2
func getWindowsHostIP() string {
	data, err := os.ReadFile("/etc/resolv.conf")
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "nameserver") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					ip := parts[1]
					if ip != "127.0.0.1" && !strings.HasPrefix(ip, "127.0.") {
						return ip
					}
				}
			}
		}
	}

	out, err := exec.Command("ip", "route", "show", "default").Output()
	if err == nil {
		fields := strings.Fields(string(out))
		for i, f := range fields {
			if f == "via" && i+1 < len(fields) {
				return fields[i+1]
			}
		}
	}

	return ""
}
