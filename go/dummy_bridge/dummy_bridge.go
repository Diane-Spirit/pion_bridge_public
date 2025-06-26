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

// StartBridge initiates a background process that simulates data streaming.
func StartBridge() {
	mu.Lock()
	defer mu.Unlock()

	// If the bridge is already running, prevent duplicate starts
	if running {
		log.Println("Bridge is already running.")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancelFn = cancel
	running = true

	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Println("Bridge has stopped.")
				return
			default:
				log.Println("Bridge is active: streaming data...")
				time.Sleep(2 * time.Second)
			}
		}
	}()

	log.Println("Bridge has started.")
}

// StopBridge stops the background process.
func StopBridge() {
	mu.Lock()
	defer mu.Unlock()

	// If no bridge is running, no need to stop
	if !running {
		log.Println("Bridge is already stopped.")
		return
	}

	// Cancel the context to stop the goroutine
	if cancelFn != nil {
		cancelFn()
	}

	running = false
	log.Println("Bridge stop request completed.")
}
