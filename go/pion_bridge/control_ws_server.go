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
    // controlConns stores all active WebSocket connections from control clients.
    // The boolean value is a placeholder and can be used for additional per-connection state if needed.
    controlConns = make(map[*websocket.Conn]bool)
    // controlMutex synchronizes access to the controlConns map.
    controlMutex sync.Mutex

    // selectedRobotId stores the ID of the robot currently selected by a control client
    // to be the active robot for streaming or interaction.
    selectedRobotId int
    // selectedRobotIdMutex synchronizes access to selectedRobotId.
	selectedRobotIdMutex sync.RWMutex

    // controlServer is the HTTP server instance for the control WebSocket endpoint.
    controlServer *http.Server
)

// controlUpgrader configures the parameters for upgrading HTTP connections to WebSocket connections.
var controlUpgrader = websocket.Upgrader{
    ReadBufferSize:  1024, // Specifies the size of the read buffer for the WebSocket connection.
    WriteBufferSize: 1024, // Specifies the size of the write buffer for the WebSocket connection.
    // CheckOrigin allows all origins. In a production environment, this should be configured
    // to allow connections only from trusted domains for security.
    // Example: CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "https://your-trusted-domain.com" }
    CheckOrigin: func(r *http.Request) bool { return true },
}

// RobotUpdate defines the structure for sending individual robot status updates to control clients.
type RobotUpdate struct {
    ID   int    `json:"id"`   // Unique identifier of the robot.
    Name string `json:"name"` // Name of the robot.

}

// ControlMessage defines the basic structure for incoming messages from control clients,
// used to determine the message type before unmarshalling the full payload.
type ControlMessage struct {
    Type string `json:"type"` // Type of the control command (e.g., "switchRobot").
}

// RobotUpdateMessage defines the structure for sending a list of robot updates to control clients.
type RobotUpdateMessage struct {
    Type   string        `json:"type"`   // Message type, typically "updateRobots".
    Robots []RobotUpdate `json:"robots"` // List of robot statuses.
}

// SwitchedRobotMessage defines the structure for notifying control clients that the selected robot has changed.
type SwitchedRobotMessage struct {
    Type    string `json:"type"`    // Message type, typically "switchedRobot".
    RobotId int    `json:"robotId"` // ID of the newly selected robot.
}

// SwitchRobotPayload defines the expected payload for a "switchRobot" command from a control client.
type SwitchRobotPayload struct {
    RobotId int `json:"robotId"` // ID of the robot to switch to.
}

// SwitchModePayload defines the expected payload for a "switchMode" command from a control client.
type SwitchModePayload struct {
    Mode string `json:"mode"` // The mode to switch to (e.g., "manualControl", "autonomousNavigation").
}

// InitBridgePayload defines the expected payload for an "initBridge" command from a control client.
type InitBridgePayload struct {
    URL string `json:"url"` // URL of the signaling server to connect to.
}

// GetSelectedRobotId safely retrieves the ID of the currently selected robot.
// It uses a read lock to allow concurrent reads without blocking writes unnecessarily.
func GetSelectedRobotId() int {
	selectedRobotIdMutex.RLock()
	defer selectedRobotIdMutex.RUnlock()
	return selectedRobotId
}

// SetSelectedRobotId safely sets the ID of the currently selected robot.
// It uses a write lock to ensure exclusive access during modification.
func SetSelectedRobotId(robotId int) {
	selectedRobotIdMutex.Lock()
	defer selectedRobotIdMutex.Unlock()
	selectedRobotId = robotId
}

// controlHandler handles incoming WebSocket connection requests for the control server.
// It upgrades the HTTP connection to a WebSocket connection and manages its lifecycle,
// including reading messages and ensuring cleanup on disconnection.
func controlHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := controlUpgrader.Upgrade(w, r, nil)
	if err != nil {
        log.Printf("Control WebSocket upgrade error for %s: %v", r.RemoteAddr, err)
		return
	}
    log.Printf("Control WebSocket client connected: %s", conn.RemoteAddr())

	controlMutex.Lock()
	controlConns[conn] = true
	controlMutex.Unlock()

    // Ensure connection is closed and removed from the map when the handler exits.
	defer func() {
		controlMutex.Lock()
		delete(controlConns, conn)
		controlMutex.Unlock()
		conn.Close()
	}()

    // Continuously read messages from the WebSocket connection.
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Println("Control WebSocket read error:", err)
			break
		}

		handleControlMessage(conn, message)
	}
}

// handleControlMessage parses the incoming message from a control client and routes it
// to the appropriate handler based on its 'type' field.
func handleControlMessage(conn *websocket.Conn, message []byte) {
	var msg ControlMessage
	if err := json.Unmarshal(message, &msg); err != nil {
        log.Printf("Error unmarshalling control message type from %s: %v. Raw message: %s", conn.RemoteAddr(), err, string(message))
		return
	}

    log.Printf("Processing control message of type '%s' from %s", msg.Type, conn.RemoteAddr())
	switch msg.Type {
	case "switchRobot":
		var payload SwitchRobotPayload
		if err := json.Unmarshal(message, &payload); err != nil {
            log.Printf("Error unmarshalling 'switchRobot' payload from %s: %v. Raw message: %s", conn.RemoteAddr(), err, string(message))
			return
		}
		handleSwitchRobot(payload)
	case "switchMode":
		var payload SwitchModePayload
		if err := json.Unmarshal(message, &payload); err != nil {
            log.Printf("Error unmarshalling 'switchMode' payload from %s: %v. Raw message: %s", conn.RemoteAddr(), err, string(message))
			return
		}
		handleSwitchMode(payload)
	case "initBridge":
		var payload InitBridgePayload
		if err := json.Unmarshal(message, &payload); err != nil {
            log.Printf("Error unmarshalling 'initBridge' payload from %s: %v. Raw message: %s", conn.RemoteAddr(), err, string(message))
			return
		}
		handleInitBridge(payload)
	case "updateRobots":
        // This case allows a client to explicitly request a robot list refresh.
		handleUpdateRobots()
	default:
        log.Printf("Received unknown control message type '%s' from %s. Raw message: %s", msg.Type, conn.RemoteAddr(), string(message))
	}
}

// handleSwitchRobot processes the "switchRobot" command.
// It updates the globally selected robot ID, sends "start"/"stop" commands
// to the respective robot peer connections via their control data channels,
// and notifies all control clients of the change.
func handleSwitchRobot(payload SwitchRobotPayload) {
	log.Println("Switching to robot:", payload.RobotId)

	// -------- TODO: Add your logic to switch robot here -------- //

	SetSelectedRobotId(payload.RobotId)

	for robotID, peerConnection := range robotPeerConnections {
		if robotID == payload.RobotId {
			peerConnection.controltrackDC.SendText("start")
		} else {
			peerConnection.controltrackDC.SendText("stop")
		}

	}

    // Prepare response message to notify all control clients about the switch.
	resp := SwitchedRobotMessage{
		Type:    "switchedRobot",
		RobotId: payload.RobotId,
	}
	respBytes, err := json.Marshal(resp)
	if err != nil {
        log.Printf("Error marshalling 'switchedRobot' response: %v", err)
		return
	}

    // Send the response to all connected control clients.
	controlMutex.Lock()
	defer controlMutex.Unlock()
    log.Printf("Broadcasting 'switchedRobot' message for robot ID %d to %d clients.", payload.RobotId, len(controlConns))
	for conn := range controlConns {
		if err := conn.WriteMessage(websocket.TextMessage, respBytes); err != nil {
            log.Printf("Error writing 'switchedRobot' response to client %s: %v", conn.RemoteAddr(), err)
            // Consider removing the connection if writing fails persistently.
		}
	}
}

// handleSwitchMode processes the "switchMode" command.
// Placeholder for mode switching logic.
func handleSwitchMode(payload SwitchModePayload) {
    log.Printf("Processing 'switchMode' command. Attempting to switch to mode: '%s'", payload.Mode)

	// -------- TODO: Add your logic to switch mode here -------- //
}

func handleInitBridge(payload InitBridgePayload) {
	log.Println("Initializing bridge with URL:", payload.URL)

    // Connect to the signaling server (ConnectToSignalingServer is assumed to be in signaling_client.go).
	if err := ConnectToSignalingServer(payload.URL); err != nil {
		log.Println("Error connecting to signaling server:", err)
	}

    // Retrieve the initial list of robots from the signaling server (GetRobots is assumed to be in signaling_client.go).
	if err := GetRobots(); err != nil {
        log.Printf("Error retrieving initial list of robots after connecting to '%s': %v", payload.URL, err)
	}
}

// handleUpdateRobots processes a request from a control client to refresh the robot list.
// It triggers a request to the signaling server for the latest robot data.
// The actual update to clients is handled by NotifyRobotsUpdate when the signaling client receives new data.
func handleUpdateRobots() {
    log.Println("Processing 'updateRobots' request by a client. Fetching latest robot list from signaling server.")

	if err := GetRobots(); err != nil {
        log.Printf("Error requesting robot list update from signaling server: %v", err)
	}
}

// NotifyRobotsUpdate is called (typically by the signaling client logic within the same package)
// when the list of available robots or their status changes.
// It formats this information and broadcasts it to all connected control clients.
func NotifyRobotsUpdate() {
	var updates []RobotUpdate
	for _, robot := range robots {
		updates = append(updates, RobotUpdate{
			ID:   robot.ID,
			Name: robot.Name,
		})
	}

    log.Printf("Preparing to send 'updateRobots' message with %d robots.", len(updates))

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

// StartControlServer initializes and starts the HTTP server for the control WebSocket endpoint.
// The server listens on the specified address.
func StartControlServer(address string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", controlHandler)

	controlServer = &http.Server{
		Addr:    address,
		Handler: mux,
	}

	log.Printf("Starting Control Server on %s\n", address)
	if err := controlServer.ListenAndServe(); err != nil {
		// If the error is Server closed do nothing as it is expected
		if err == http.ErrServerClosed {
			return
		}

		log.Fatalf("Control Server failed: %v\n", err)
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

func CloseControlServer() {
	CloseControlConnections()

	if err := controlServer.Shutdown(context.Background()); err != nil {
		log.Println("Control server shutdown error:", err)
	} else {
		log.Println("Control server shutdown gracefully")
	}
}