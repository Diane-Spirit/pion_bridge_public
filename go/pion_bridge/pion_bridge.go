package bridge

import (
    "context"
    "log"
    "sync"
)

var (
	mu       sync.Mutex
    cancelFn context.CancelFunc
    // running indicates whether the bridge is currently active.
    // It's used to prevent starting an already running bridge or stopping an already stopped one.
    running bool
)

// StartBridge initializes and starts the main components of the Pion bridge.
// These components typically include control, telemetry, and streaming servers.
// It ensures that the bridge is not started if it's already running.
func StartBridge() {
    mu.Lock() // Lock to ensure exclusive access to 'running' and 'cancelFn'.
    defer mu.Unlock()

    if running {
        log.Println("Bridge is already running. Start request ignored.")
        return
    }

    // Create a new context with a cancel function for managing the lifecycle of the bridge components.
    // The 'ctx' variable itself is not explicitly used here, but 'cancelFn' is stored.
    // For a more complete implementation, 'ctx' would be passed to the server start functions.
    _, cancel := context.WithCancel(context.Background())
    cancelFn = cancel // Store the cancel function to be called by StopBridge.
    running = true    // Set the state to running.

    // Start the control server in a new goroutine.
    // This server handles commands and interactions with control clients.
    go func() {
        // "localhost:8085" is the address where the control server will listen.
        StartControlServer("localhost:8085")
    }()

    // Start the telemetry server in a new goroutine.
    go func() {
        // "localhost:8090" is the address for the telemetry server.
        StartTelemetryServer("localhost:8090")
    }()

    // Start the streaming server in a new goroutine.
    go func() {
        // "localhost:8095" is the address for the streaming server.
        StartStreamingServer("localhost:8095")
    }()

    log.Println("Bridge has been started successfully.")
}

// StopBridge gracefully shuts down all components of the Pion bridge.
// It calls the context cancellation function and explicitly closes connections
// for each server. It ensures that the bridge is not stopped if it's already stopped.
func StopBridge() {
    mu.Lock() // Lock to ensure exclusive access to 'running' and 'cancelFn'.
    defer mu.Unlock()

    if !running {
        log.Println("Bridge is already stopped. Stop request ignored.")
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
