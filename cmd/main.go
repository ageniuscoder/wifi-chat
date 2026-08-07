package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"wifi-chat/internal/server"
)

func main() {
	port := flag.Int("port", 8000, "Server port")
	peer := flag.String("peer", "", "Upstream peer URL (e.g. ws://192.168.1.10:8000/ws/mesh)")
	nodeName := flag.String("node", "", "Node name for mesh (auto-generated if empty)")
	flag.Parse()

	name := *nodeName
	if name == "" {
		name = fmt.Sprintf("node-%04x", rand.Intn(0xFFFF))
	}

	srv := server.NewServer(*port, name)

	// Connect to upstream mesh peer(s)
	if *peer != "" {
		for _, p := range strings.Split(*peer, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				log.Printf("[MESH] Will connect to peer: %s", p)
				srv.ConnectPeer(p)
			}
		}
	}

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		log.Println("Shutting down gracefully...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		srv.Shutdown()
		if srv.HTTPServer != nil {
			srv.HTTPServer.Shutdown(ctx)
		}
		os.Exit(0)
	}()

	log.Fatal(srv.Start())
}
