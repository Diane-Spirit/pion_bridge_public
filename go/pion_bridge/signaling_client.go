package bridge

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
)

var (
	// robots stores the currently known robots, keyed by their ID.
	robots = make(map[int]Robot)
	// clientConn is the WebSocket connection to the signaling server.
	clientConn *websocket.Conn
	// clientConnMux protects access to clientConn.
	clientConnMux sync.Mutex
)

// Robot defines the structure for robot data exchanged with the signaling server.
type Robot struct {
	ID               int           `json:"id"`
	Name             string        `json:"name"`
	SdpOffer         string        `json:"sdpOffer"`
	SdpAnswer        string        `json:"sdpAnswer"`
	RobotCandidates  []interface{} `json:"robotCandidates"`  // ICE candidates from the robot
	ClientCandidates []interface{} `json:"clientCandidates"` // ICE candidates from this client
}

// RobotDeregistered is used to unmarshal messages indicating a robot has been deregistered.
type RobotDeregistered struct {
	RobotId int `json:"robotId"`
}

// CommandMessage defines the basic structure for messages sent to and received from the signaling server.
type CommandMessage struct {
	Type string `json:"type"` // Type of the command, e.g., "robots", "offer", "answer"
}

// ConnectToSignalingServer establishes a WebSocket connection to the specified signaling server URL.
// It starts a goroutine to read incoming messages upon successful connection.
func ConnectToSignalingServer(url string) error {
	log.Printf("Attempting to connect to signaling server at %s", url)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to signaling server: %w", err)
	}

	clientConnMux.Lock()
	clientConn = conn
	clientConnMux.Unlock()

	log.Printf("Successfully connected to signaling server at %s", url)
	go readMessages() // Start listening for messages in a new goroutine
	return nil
}

// readMessages reads messages from the WebSocket connection.
func readMessages() {
	for {
		_, message, err := clientConn.ReadMessage()
		if err != nil {
			log.Println("read error:", err)
			return
		}
		handleMessage(message)
	}
}

// handleMessage unmarshals the incoming WebSocket message to determine its type
// and delegates it to the appropriate handler function.
func handleMessage(message []byte) {
	var msg CommandMessage
	if err := json.Unmarshal(message, &msg); err != nil {
		log.Printf("Error unmarshalling message type: %v, raw message: %s", err, string(message))
		return
	}

	switch msg.Type {
	case "robots":
		var payload struct {
			Robots []Robot `json:"robots"`
		}
		if err := json.Unmarshal(message, &payload); err != nil {
			log.Printf("Error unmarshalling 'robots' payload: %v", err)
			return
		}
		handleRobotsMessage(payload.Robots)
	case "register":
		var payload struct {
			Robot Robot `json:"robot"`
		}
		if err := json.Unmarshal(message, &payload); err != nil {
			log.Printf("Error unmarshalling 'register' payload: %v", err)
			return
		}
		handleRegisterMessage(payload.Robot)
	case "offer":
		var payload struct {
			RobotID  int    `json:"robotId"`
			SdpOffer string `json:"sdpOffer"`
		}
		if err := json.Unmarshal(message, &payload); err != nil {
			log.Printf("Error unmarshalling 'offer' payload: %v", err)
			return
		}
		handleOfferMessage(payload.RobotID, payload.SdpOffer)
	case "answer":
		var payload struct {
			RobotID   int    `json:"robotId"`
			SdpAnswer string `json:"sdpAnswer"`
		}
		if err := json.Unmarshal(message, &payload); err != nil {
			log.Printf("Error unmarshalling 'answer' payload: %v", err)
			return
		}
		handleAnswerMessage(payload.RobotID, payload.SdpAnswer)
	case "candidate":
		var payload struct {
			RobotID   int    `json:"robotId"`
			Candidate string `json:"candidate"`
		}
		if err := json.Unmarshal(message, &payload); err != nil {
			log.Printf("Error unmarshalling 'candidate' payload: %v", err)
			return
		}
		handleCandidateMessage(payload.RobotID, payload.Candidate)
	case "deregistered":
		var payload RobotDeregistered
		if err := json.Unmarshal(message, &payload); err != nil {
			log.Printf("Error unmarshalling 'deregistered' payload: %v", err)
			return
		}
		handleDeregisterMessage(payload.RobotId)
	default:
		log.Printf("Received unknown message type: %s", msg.Type)
	}
}

// handleRobotsMessage processes a list of robots received from the signaling server.
// It updates the local 'robots' map, establishes new peer connections for new robots,
// checks and potentially re-establishes connections for existing robots if they are in a failed state,
// and removes robots that are no longer present in the received list.
func handleRobotsMessage(robotsPayload []Robot) {
	log.Printf("Processing 'robots' message with %d robots in payload.", len(robotsPayload))
	// Build a set of robot IDs from the incoming payload for efficient lookup.
	newRobotIDs := make(map[int]bool)
	for _, robot := range robotsPayload {
		newRobotIDs[robot.ID] = true

		shouldConnect := false

		// Check if the robot is new or if its existing connection needs re-establishment.
		if _, exists := robots[robot.ID]; !exists {
			shouldConnect = true
			log.Printf("New robot detected: ID %d, Name '%s'", robot.ID, robot.Name)
		} else {
			// For existing robots, check if their peer connection is valid.
			// Assumes robotPeerConnections map is maintained elsewhere.
			pc, pcExists := robotPeerConnections[robot.ID]
			if !pcExists || pc == nil || pc.state == webrtc.PeerConnectionStateFailed || pc.state == webrtc.PeerConnectionStateClosed {
				shouldConnect = true
				if !pcExists || pc == nil {
					log.Printf("Missing peer connection for existing robot %d. Will attempt to connect.", robot.ID)
				} else {
					log.Printf("Peer connection for robot %d is in state %s. Will attempt to reconnect.", robot.ID, pc.state.String())
				}
			} else {
				log.Printf("Robot %d (%s) is already connected and its peer connection is in state %s.", robot.ID, robot.Name, pc.state.String())
			}
		}

		// Update the local robot record.
		robots[robot.ID] = robot

		// Establish or re-establish connection if required.
		if shouldConnect {
			log.Printf("Establishing/Re-establishing peer connection for robot %d (%s).", robot.ID, robot.Name)
			addRobotPeerConnection(robot.ID) // Assumes this function handles creation and setup.
			if robot.SdpOffer != "" {
				log.Printf("Robot %d (%s) has an existing SDP offer. Processing it.", robot.ID, robot.Name)
				answer := addOfferRobotPeerConnection(robot.ID, robot.SdpOffer) // Assumes this processes offer and returns answer.
				if err := SetSdpAnswer(robot.ID, answer); err != nil {
					log.Printf("Error sending SDP answer for robot %d: %v", robot.ID, err)
				}
			}
		}
	}

	// Remove robots that are in the local 'robots' map but not in the new payload.
	for id, robot := range robots {
		if !newRobotIDs[id] {
			log.Printf("Removing robot %d (%s) as it is no longer present in the received list.", id, robot.Name)
			delete(robots, id)
			// Optionally: close and remove the associated peer connection.
			if pc, exists := robotPeerConnections[id]; exists && pc != nil {
				log.Printf("Closing peer connection for removed robot %d.", id)
				// pc.Close() // Assuming PeerConnection has a Close method.
				delete(robotPeerConnections, id)
			}
		}
	}

	NotifyRobotsUpdate() // Notify other parts of the application about the updated robot list.
	log.Printf("Finished processing 'robots' message. Current number of robots: %d", len(robots))
}

// handleRegisterMessage processes a 'register' message for a single robot.
// It adds the robot to the local map and establishes a peer connection.
// If an SDP offer is included, it processes the offer and sends an answer.
func handleRegisterMessage(robot Robot) {
	log.Printf("Processing 'register' message for robot ID %d, Name '%s'.", robot.ID, robot.Name)
	robots[robot.ID] = robot
	addRobotPeerConnection(robot.ID)
	NotifyRobotsUpdate()
	log.Printf("Robot %d (%s) registered.", robot.ID, robot.Name)

	if robot.SdpOffer != "" {
		log.Printf("Processing SDP offer included with registration for robot %d.", robot.ID)
		answer := addOfferRobotPeerConnection(robot.ID, robot.SdpOffer)
		SetSdpAnswer(robot.ID, answer)
		log.Println("SDP inviato")
	}
}

// handleDeregisterMessage processes a 'deregistered' message for a robot.
// It removes the robot from the local map and handles cleanup of its peer connection.
func handleDeregisterMessage(robotID int) {
	log.Printf("Processing 'deregistered' message for robot ID %d.", robotID)
	if robot, exists := robots[robotID]; exists {
		delete(robots, robotID)
		if pc, pcExists := robotPeerConnections[robotID]; pcExists && pc != nil {
			log.Printf("Closing peer connection for deregistered robot %d (%s).", robotID, robot.Name)
			// pc.Close() // Assuming PeerConnection has a Close method.
			delete(robotPeerConnections, robotID)
		}
		NotifyRobotsUpdate()
		log.Printf("Robot %d (%s) deregistered. Updated robots list. Current count: %d", robotID, robot.Name, len(robots))
	} else {
		log.Printf("Received deregister message for unknown robot ID %d.", robotID)
	}
}

// handleOfferMessage processes an SDP offer received from a robot.
// It updates the robot's SdpOffer field, processes the offer to generate an answer,
// and sends the SDP answer back to the signaling server.
func handleOfferMessage(robotID int, sdpOffer string) {
	log.Printf("Processing 'offer' message for robot ID %d.", robotID)
	if robot, exists := robots[robotID]; exists {
		robot.SdpOffer = sdpOffer
		robots[robotID] = robot // Update the map with the new offer
		answer := addOfferRobotPeerConnection(robotID, sdpOffer)
		SetSdpAnswer(robotID, answer)
		NotifyRobotsUpdate()
		log.Println("Updated SDP offer for robot:", robotID)
	}
}

// handleAnswerMessage processes an SDP answer received from a robot (or server on behalf of robot).
// It updates the robot's SdpAnswer field. This is typically used if this client initiated an offer.
func handleAnswerMessage(robotID int, sdpAnswer string) {
	log.Printf("Processing 'answer' message for robot ID %d.", robotID)
	if robot, exists := robots[robotID]; exists {
		robot.SdpAnswer = sdpAnswer
		robots[robotID] = robot
		NotifyRobotsUpdate()
		log.Printf("SDP answer from robot %d processed.", robotID)
	} else {
		log.Printf("Received SDP answer for unknown robot ID %d.", robotID)
	}
}

func handleCandidateMessage(robotID int, candidate string) {
	if robot, exists := robots[robotID]; exists {
		robot.ClientCandidates = append(robot.ClientCandidates, candidate)
		robots[robotID] = robot
		AddICECandidateRobotPeerConnection(robotID, candidate)
		NotifyRobotsUpdate()
		log.Println("Updated ICE candidates for robot:", robotID)
	}
}

// GetRobots sends a "getRobots" command to the signaling server to request the current list of robots.
func GetRobots() error {
	log.Println("Sending 'getRobots' request to signaling server.")
	msg := CommandMessage{Type: "getRobots"}
	return sendMessage(msg)
}

// SetSdpAnswer sends an SDP answer to the signaling server for a specific robot.
func SetSdpAnswer(robotID int, sdpAnswer string) error {
	log.Printf("Sending SDP answer for robot ID %d.", robotID)
	answerMsg := struct {
		Type      string `json:"type"`
		RobotId   int    `json:"robotId"`
		SdpAnswer string `json:"sdpAnswer"`
	}{
		Type:      "answer",
		RobotId:   robotID,
		SdpAnswer: sdpAnswer,
	}

	clientConnMux.Lock()
	defer clientConnMux.Unlock()
	if clientConn == nil {
		return fmt.Errorf("WebSocket connection is not established")
	}

	message, err := json.Marshal(answerMsg)
	if err != nil {
		return fmt.Errorf("failed to marshal SDP answer message: %w", err)
	}
	return clientConn.WriteMessage(websocket.TextMessage, message)
}

func AddIceCandidate(robotID int, candidate string) error {
	candidateMsg := struct {
		Type      string `json:"type"`
		RobotId   int    `json:"robotId"`
		Candidate string `json:"candidate"`
	}{
		Type:      "candidate",
		RobotId:   robotID,
		Candidate: candidate,
	}

	clientConnMux.Lock()
	defer clientConnMux.Unlock()
	if clientConn == nil {
		return fmt.Errorf("WebSocket connection is not established")
	}

	message, err := json.Marshal(candidateMsg)
	if err != nil {
		return fmt.Errorf("failed to marshal candidate message: %w", err)
	}
	return clientConn.WriteMessage(websocket.TextMessage, message)
}

// sendMessage sends a message to the signaling server.
func sendMessage(msg CommandMessage) error {
	clientConnMux.Lock()
	defer clientConnMux.Unlock()
	if clientConn == nil {
		return fmt.Errorf("WebSocket connection is not established")
	}
	message, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}
	return clientConn.WriteMessage(websocket.TextMessage, message)
}

// sendMessageWithPayload sends a message with a payload to the signaling server.
func sendMessageWithPayload(msg CommandMessage, payload string) error {
	clientConnMux.Lock()
	defer clientConnMux.Unlock()
	if clientConn == nil {
		return fmt.Errorf("WebSocket connection is not established")
	}
	message := fmt.Sprintf(`{"type": "%s", %s}`, msg.Type, payload)
	return clientConn.WriteMessage(websocket.TextMessage, []byte(message))
}
