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

	"github.com/joho/godotenv"
)

func main() {
	//load env file
	err := godotenv.Load()
	if err != nil {
		log.Fatalf("Error loading .env file: %s", err.Error())
	}

	serverCfg := server.LoadConfig()

	//get flags from terminal if provided
	port := flag.Int("port", int(serverCfg.Port), "Server port")
	peer := flag.String("peer", "", "Upstream peer URL (e.g. ws://192.168.1.10:8000/ws/mesh)")
	nodeName := flag.String("node", "", "Node name for mesh (auto-generated if empty)")

	//parse it before using it
	flag.Parse()

	//ensure name is not empty
	name := *nodeName
	if name == "" {
		name = fmt.Sprintf("node-%04x", rand.Intn(0xFFFF)) //4 digit of hexadecimal number padded with zeroes %04x
	}
	//start the new server
	srv := server.NewServer(*port, name, serverCfg)

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
	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-signalCtx.Done()

		log.Println("Shutting down gracefully...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		srv.Shutdown()
		if srv.HTTPServer != nil {
			if err := srv.HTTPServer.Shutdown(ctx); err != nil {
				log.Printf("HTTP server shutdown error: %v", err.Error())
			}
		}
		os.Exit(0)
	}()

	log.Fatal(srv.Start())
}
