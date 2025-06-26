package bridge

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var (
	// WSServer holds a reference to the running http.Server.
	WSServer *http.Server

	// activeConns store active WebSocket connections.
	activeConns = make(map[*websocket.Conn]bool)
	connMutex   sync.Mutex
)

// upgrader upgrades HTTP connections to WebSocket connections.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  10 * 1024 * 1024,
	WriteBufferSize: 10 * 1024 * 1024,
	// Allow all origins for simplicity.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// wsHandler handles an individual WebSocket connection.
func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("WebSocket upgrade error:", err)
		return
	}

	// Register connection.
	connMutex.Lock()
	activeConns[conn] = true
	connMutex.Unlock()

	defer func() {
		// Unregister connection.
		connMutex.Lock()
		delete(activeConns, conn)
		connMutex.Unlock()
		conn.Close()
	}()

	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			log.Println("WebSocket read error:", err)
			break
		}

		log.Println("Received message")
		if err = conn.WriteMessage(messageType, message); err != nil {
			log.Println("WebSocket write error:", err)
			break
		}
	}
}

// StartWebSocketServer starts a WebSocket server on the given address.
// It listens for the provided context cancellation to shut down gracefully.
func StartWebSocketServer(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", wsHandler)

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	// Store reference to allow external shutdown.
	WSServer = server

	// Listen for cancellation of context to shutdown.
	go func() {
		<-ctx.Done()
		ctxShutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctxShutdown); err != nil {
			log.Println("WebSocket server shutdown error:", err)
		} else {
			log.Println("WebSocket server shutdown gracefully")
		}
	}()

	log.Println("Starting WebSocket server on", "ws://"+addr)
	err := server.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		log.Println("WebSocket server ListenAndServe error:", err)
		return err
	}

	return nil
}

// CloseAllConnections iterates over and closes all active WebSocket connections.
func CloseAllConnections() {
	connMutex.Lock()
	defer connMutex.Unlock()
	for conn := range activeConns {
		_ = conn.Close()
		delete(activeConns, conn)
	}
}