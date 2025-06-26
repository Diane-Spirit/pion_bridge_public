package main

import (
	"context"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var (
	streamingConns  = make(map[*websocket.Conn]bool)
	streamingMutex  sync.Mutex
	streamingServer *http.Server
)

var streamingUpgrader = websocket.Upgrader{
	ReadBufferSize:  10 * 1024 * 1024,
	WriteBufferSize: 10 * 1024 * 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func streamingHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := streamingUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Streaming WebSocket upgrade error:", err)
		return
	}

	streamingMutex.Lock()
	streamingConns[conn] = true
	streamingMutex.Unlock()

	defer func() {
		streamingMutex.Lock()
		delete(streamingConns, conn)
		streamingMutex.Unlock()
		conn.Close()
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Println("Streaming WebSocket read error:", err)
			break
		}

		handleStreamingMessage(conn, message)
	}
}

func handleStreamingMessage(conn *websocket.Conn, message []byte) {
	// Handle streaming messages (e.g., updatePoints)
	log.Println("Received streaming message:", string(message))
	// Add your streaming message handling logic here
}

func SendFrameToClients(frame []byte) {
	streamingMutex.Lock()
	defer streamingMutex.Unlock()
	for conn := range streamingConns {
		if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
			log.Printf("Failed to send frame to client: %v\n", err)
		}
	}
}

func StartStreamingServer(address string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", streamingHandler)

	streamingServer = &http.Server{
		Addr:    address,
		Handler: mux,
	}

	log.Printf("Starting Streaming Server on %s\n", address)
	if err := streamingServer.ListenAndServe(); err != nil {
		// If the error is Server closed do nothing as it is expected
		if err == http.ErrServerClosed {
			return
		}

		log.Fatalf("Streaming Server failed: %v\n", err)
	}
}

func CloseStreamingConnections() {
	streamingMutex.Lock()
	defer streamingMutex.Unlock()
	for conn := range streamingConns {
		_ = conn.Close()
		delete(streamingConns, conn)
	}
}

func CloseStreamingServer() {
	CloseStreamingConnections()

	if err := streamingServer.Shutdown(context.Background()); err != nil {
		log.Println("Streaming server shutdown error:", err)
	} else {
		log.Println("Streaming server shutdown gracefully")
	}
}
