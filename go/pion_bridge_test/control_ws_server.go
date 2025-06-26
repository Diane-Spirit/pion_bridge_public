package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var (
	controlConns         = make(map[*websocket.Conn]bool)
	controlMutex         sync.Mutex
	selectedRobotId      int
	selectedRobotIdMutex sync.RWMutex
)

var controlUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

type RobotUpdate struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type ControlMessage struct {
	Type string `json:"type"`
}

type RobotUpdateMessage struct {
	Type   string        `json:"type"`
	Robots []RobotUpdate `json:"robots"`
}

type SwitchedRobotMessage struct {
	Type    string `json:"type"`
	RobotId int    `json:"robotId"`
}

type SwitchRobotPayload struct {
	RobotId int `json:"robotId"`
}

type SwitchModePayload struct {
	Mode string `json:"mode"`
}

type InitBridgePayload struct {
	URL string `json:"url"`
}

func GetSelectedRobotId() int {
	selectedRobotIdMutex.RLock()
	defer selectedRobotIdMutex.RUnlock()
	return selectedRobotId
}
func SetSelectedRobotId(robotId int) {
	selectedRobotIdMutex.Lock()
	selectedRobotId = robotId
	selectedRobotIdMutex.Unlock()
}

func controlHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := controlUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Control WebSocket upgrade error:", err)
		return
	}

	controlMutex.Lock()
	controlConns[conn] = true
	controlMutex.Unlock()

	defer func() {
		controlMutex.Lock()
		delete(controlConns, conn)
		controlMutex.Unlock()
		conn.Close()
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Println("Control WebSocket read error:", err)
			break
		}

		handleControlMessage(conn, message)
	}
}

func handleControlMessage(conn *websocket.Conn, message []byte) {
	var msg ControlMessage
	if err := json.Unmarshal(message, &msg); err != nil {
		log.Println("Error unmarshalling control message:", err)
		return
	}

	switch msg.Type {
	case "switchRobot":
		var payload SwitchRobotPayload
		if err := json.Unmarshal(message, &payload); err != nil {
			log.Println("Error unmarshalling switchRobot payload:", err)
			return
		}
		handleSwitchRobot(payload)
	case "switchMode":
		var payload SwitchModePayload
		if err := json.Unmarshal(message, &payload); err != nil {
			log.Println("Error unmarshalling switchMode payload:", err)
			return
		}
		handleSwitchMode(payload)
	case "initBridge":
		var payload InitBridgePayload
		if err := json.Unmarshal(message, &payload); err != nil {
			log.Println("Error unmarshalling initBridge payload:", err)
			return
		}
		handleInitBridge(payload)
	case "updateRobots":
		handleUpdateRobots()
	default:
		log.Println("Unknown message type:", msg.Type)
	}
}

func handleSwitchRobot(payload SwitchRobotPayload) {
	log.Println("Switching to robot:", payload.RobotId)

	SetSelectedRobotId(payload.RobotId)

	// Send response to all control connections
	resp := SwitchedRobotMessage{
		Type:    "switchedRobot",
		RobotId: payload.RobotId,
	}

	respBytes, err := json.Marshal(resp)
	if err != nil {
		log.Println("Error marshalling response:", err)
		return
	}

	controlMutex.Lock()
	defer controlMutex.Unlock()
	for conn := range controlConns {
		if err := conn.WriteMessage(websocket.TextMessage, respBytes); err != nil {
			log.Println("Error writing response:", err)
		}
	}
}

func handleSwitchMode(payload SwitchModePayload) {
	log.Println("Switching mode to:", payload.Mode)

}

func handleInitBridge(payload InitBridgePayload) {
	log.Println("Initializing bridge with URL:", payload.URL)

	// Connect to the signaling server
	if err := ConnectToSignalingServer(payload.URL); err != nil {
		log.Println("Error connecting to signaling server:", err)
	}

	// Retrieve robots
	if err := GetRobots(); err != nil {
		log.Println("Error retrieving robots:", err)
	}
}

func handleUpdateRobots() {
	log.Println("Updating robots")

	// Retrieve robots
	if err := GetRobots(); err != nil {
		log.Println("Error retrieving robots:", err)
	}
}

// sends the updated robot list to all control connections.
func NotifyRobotsUpdate() {
	var updates []RobotUpdate
	for _, robot := range robots {
		updates = append(updates, RobotUpdate{
			ID:   robot.ID,
			Name: robot.Name,
		})
	}

	log.Println("Sending robot update message")

	message, err := json.Marshal(
		RobotUpdateMessage{
			Type:   "updateRobots",
			Robots: updates,
		})
	if err != nil {
		log.Println("Error marshalling robot update message:", err)
		return
	}

	controlMutex.Lock()
	defer controlMutex.Unlock()
	for conn := range controlConns {
		if err := conn.WriteMessage(websocket.TextMessage, message); err != nil {
			log.Println("Error writing robot update message:", err)
		}
	}
}

func StartControlServer(address string, test *bool) {

	if test != nil && *test {
		if err := ConnectToSignalingServer(""); err != nil {
			log.Println("Error connecting to signaling server:", err)
		}
	} else {

		mux := http.NewServeMux()
		mux.HandleFunc("/", controlHandler)

		server := &http.Server{
			Addr:    address,
			Handler: mux,
		}

		log.Printf("Starting Control Server on %s\n", address)
		if err := server.ListenAndServe(); err != nil {
			log.Fatalf("Control Server failed: %v\n", err)
		}
	}

}

func CloseControlConnections() {
	controlMutex.Lock()
	defer controlMutex.Unlock()
	for conn := range controlConns {
		_ = conn.Close()
		delete(controlConns, conn)
	}
}
