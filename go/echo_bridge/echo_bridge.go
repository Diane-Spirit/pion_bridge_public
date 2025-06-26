package bridge

import (
	"context"
	"log"
	"sync"
	"time"
)

var (
	mu       sync.Mutex
	cancelFn context.CancelFunc
	running  bool
)

// StartBridge starts the WebSocket server in a background goroutine and sets up the environment.
func StartBridge() {
	mu.Lock()
	defer mu.Unlock()

	if running {
		log.Println("Bridge is already running.")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancelFn = cancel
	running = true

	go func() {
		if err := StartWebSocketServer(ctx, "localhost:8080"); err != nil {
			log.Println("Error running WebSocket server:", err)
		}
	}()

	log.Println("Bridge has started.")
}

// StopBridge stops the WebSocket server by cancelling the context and explicitly shutting down the server.
// It also iterates through all active WebSocket connections and closes them.
func StopBridge() {
	mu.Lock()
	defer mu.Unlock()

	if !running {
		log.Println("Bridge is already stopped.")
		return
	}

	// Cancel the context used in StartBridge.
	if cancelFn != nil {
		cancelFn()
	}

	// Ensure the server is shut down.
	if WSServer != nil {
		ctxShutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := WSServer.Shutdown(ctxShutdown); err != nil {
			log.Println("Error during server shutdown:", err)
		} else {
			log.Println("Server shutdown gracefully (from StopBridge)")
		}
		WSServer = nil
	}

	// Close any remaining open WebSocket connections.
	CloseAllConnections()

	running = false
	log.Println("Bridge has been stopped and reset.")
}