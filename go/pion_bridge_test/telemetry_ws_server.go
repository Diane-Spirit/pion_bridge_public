package main

import (
    "context"
    "log"
    "net/http"
    "sync"

    "github.com/gorilla/websocket"
)

var (
    telemetryConns  = make(map[*websocket.Conn]bool)
    telemetryMutex  sync.Mutex
    telemetryServer *http.Server
)

var telemetryUpgrader = websocket.Upgrader{
    ReadBufferSize:  1024,
    WriteBufferSize: 1024,
    CheckOrigin:     func(r *http.Request) bool { return true },
}

func telemetryHandler(w http.ResponseWriter, r *http.Request) {
    conn, err := telemetryUpgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Println("Telemetry WebSocket upgrade error:", err)
        return
    }

    telemetryMutex.Lock()
    telemetryConns[conn] = true
    telemetryMutex.Unlock()

    defer func() {
        telemetryMutex.Lock()
        delete(telemetryConns, conn)
        telemetryMutex.Unlock()
        conn.Close()
    }()

    for {
        _, message, err := conn.ReadMessage()
        if err != nil {
            log.Println("Telemetry WebSocket read error:", err)
            break
        }

        handleTelemetryMessage(conn, message)
    }
}

func handleTelemetryMessage(conn *websocket.Conn, message []byte) {
    // Handle telemetry messages (e.g., command)
    log.Println("Received telemetry message:", string(message))
    // Add your telemetry message handling logic here
}

func StartTelemetryServer(address string) {
    mux := http.NewServeMux()
    mux.HandleFunc("/", telemetryHandler)

    telemetryServer = &http.Server{
        Addr:    address,
        Handler: mux,
    }

    log.Printf("Starting Telemetry Server on %s\n", address)
    if err := telemetryServer.ListenAndServe(); err != nil {
        // If the error is Server closed do nothing as it is expected
        if err == http.ErrServerClosed {
            return
        }

        log.Fatalf("Telemetry Server failed: %v\n", err)
    }
}

func CloseTelemetryConnections() {
    telemetryMutex.Lock()
    defer telemetryMutex.Unlock()
    for conn := range telemetryConns {
        _ = conn.Close()
        delete(telemetryConns, conn)
    }
}

func CloseTelemetryServer() {
    CloseTelemetryConnections()

    if err := telemetryServer.Shutdown(context.Background()); err != nil {
        log.Println("Telemetry server shutdown error:", err)
    } else {
        log.Println("Telemetry server shutdown gracefully")
    }
}