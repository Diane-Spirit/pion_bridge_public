package bridge

import (
	"context"
	"encoding/json"
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

type Vector3 struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

type CommandData struct {
	Linear  Vector3 `json:"linear"`
	Angular Vector3 `json:"angular"`
}

type TelemetryMessage struct {
	Type    string      `json:"type"`
	Command CommandData `json:"command"`
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

		handleTelemetryMessage(message)
	}
}

func handleTelemetryMessage(message []byte) {
	// Handle telemetry messages (e.g., command)
	var telemetryMsg TelemetryMessage
	err := json.Unmarshal(message, &telemetryMsg)
	if err != nil {
		log.Println("Error unmarshalling telemetry message:", err)
		return
	}

	if telemetryMsg.Type == "command" {
		// Estrai l'oggetto Command
		commandToForward := telemetryMsg.Command

		// Ora puoi usare commandToForward come nell'esempio di test
		// Per esempio, per inviarlo da qualche parte, prima lo codifichi in JSON:
		jsonData, err := json.Marshal(commandToForward)
		if err != nil {
			log.Println("Error marshalling command data:", err)
			return
		}

		currentSelectedRobotId := GetSelectedRobotId()
		if robotPeerConnections[currentSelectedRobotId] == nil {
			log.Println("No peer connection found for robot ID:", currentSelectedRobotId)
			return
		}

		err = robotPeerConnections[currentSelectedRobotId].dataChannel.Send(jsonData)

		if err != nil {
			log.Println("Error sending data channel message:", err)
			return
		}

	} else {
		log.Println("Received telemetry message of unknown type:", telemetryMsg.Type)
	}

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

	if telemetryServer != nil {
		if err := telemetryServer.Shutdown(context.Background()); err != nil {
			log.Println("Telemetry server shutdown error:", err)
		} else {
			log.Println("Telemetry server shutdown gracefully")
		}
	}
}
