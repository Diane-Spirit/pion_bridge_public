package main

import (
	"context"
	"log"
	"sync"
)

var (
	mu       sync.Mutex
	cancelFn context.CancelFunc
	running  bool
)

func StartBridge(test *bool) {
	mu.Lock()
	defer mu.Unlock()

	if running {
		log.Println("Bridge is already running.")
		return
	}

	_, cancel := context.WithCancel(context.Background())
	cancelFn = cancel
	running = true

	go func() {
		StartControlServer("localhost:8085", test)
	}()
	go func() {
		StartTelemetryServer("localhost:8090")
	}()
	go func() {
		StartStreamingServer("localhost:8095")
	}()

	log.Println("Bridge has started.")
}

func StopBridge() {
	mu.Lock()
	defer mu.Unlock()

	if !running {
		log.Println("Bridge is already stopped.")
		return
	}

	if cancelFn != nil {
		cancelFn()
	}

	CloseControlConnections()
	CloseTelemetryConnections()
	CloseStreamingConnections()

	running = false
	log.Println("Bridge has been stopped and reset.")
}
