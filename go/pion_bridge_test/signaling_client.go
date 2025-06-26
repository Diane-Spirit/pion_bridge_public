package main

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
)

var (
	robots        = make(map[int]Robot)
	clientConn    *websocket.Conn
	clientConnMux sync.Mutex
)

type Robot struct {
	ID               int           `json:"id"`
	Name             string        `json:"name"`
	SdpOffer         string        `json:"sdpOffer"`
	SdpAnswer        string        `json:"sdpAnswer"`
	RobotCandidates  []interface{} `json:"robotCandidates"`
	ClientCandidates []interface{} `json:"clientCandidates"`
}

type CommandMessage struct {
	Type string `json:"type"`
}
type RobotDeregistered struct {
	RobotId int `json:"robotId"`
}

// ConnectToSignalingServer connects to the signaling server via WebSocket.
func ConnectToSignalingServer(url string) error {

	cfg, err := GetConfig()
	if err != nil {
		log.Fatalf("Failed to get config: %v", err)
	}
	wbsCfg := cfg.WebSocket

	if url == "" {
		url = "ws://" + wbsCfg.Address
	}
	log.Println("Connecting to signaling server at:", url)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to signaling server: %w", err)
	}
	clientConnMux.Lock()
	clientConn = conn
	clientConnMux.Unlock()
	go readMessages()
	if err := GetRobots(); err != nil {
		log.Println("Error retrieving robots:", err)
	}

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

// handleMessage handles incoming messages from the WebSocket connection.
func handleMessage(message []byte) {
	var msg CommandMessage
	if err := json.Unmarshal(message, &msg); err != nil {
		log.Println("Error unmarshalling message:", err)
		return
	}

	switch msg.Type {
	case "robots":
		robots = make(map[int]Robot)

		var payload struct {
			Robots []Robot `json:"robots"`
		}
		if err := json.Unmarshal(message, &payload); err != nil {
			log.Println("Error unmarshalling robots payload:", err)
			return
		}
		handleRobotsMessage(payload.Robots)
	case "register":
		var payload struct {
			Robot Robot `json:"robot"`
		}
		if err := json.Unmarshal(message, &payload); err != nil {
			log.Println("Error unmarshalling register payload:", err)
			return
		}
		handleRegisteredMessage(payload.Robot)
	case "offer":
		var payload struct {
			RobotID  int    `json:"robotId"`
			SdpOffer string `json:"sdpOffer"`
		}
		if err := json.Unmarshal(message, &payload); err != nil {
			log.Println("Error unmarshalling offer payload:", err)
			return
		}
		handleOfferMessage(payload.RobotID, payload.SdpOffer)
	case "answer":
		var payload struct {
			RobotID   int    `json:"robotId"`
			SdpAnswer string `json:"sdpAnswer"`
		}
		if err := json.Unmarshal(message, &payload); err != nil {
			log.Println("Error unmarshalling answer payload:", err)
			return
		}
		handleAnswerMessage(payload.RobotID, payload.SdpAnswer)
	case "candidate":
		var payload struct {
			RobotID   int    `json:"robotId"`
			Candidate string `json:"candidate"`
		}
		if err := json.Unmarshal(message, &payload); err != nil {
			log.Println("Error unmarshalling candidate payload:", err)
			return
		}
		handleCandidateMessage(payload.RobotID, payload.Candidate)
	case "deregistered":
		var payload RobotDeregistered // Unmarshal direttamente in questa struct
		if err := json.Unmarshal(message, &payload); err != nil {
			log.Println("Error unmarshalling deregister payload:", err)
			return
		}
		handleDeregisterMessage(payload.RobotId)
	default:
		log.Println("Unknown message type:", msg.Type)
	}
}

// handleRobotsMessage compares the incoming robot list with the current records.
// It establishes new connections for new robots or reconnects robots with missing
// or inactive peer connections and removes robots no longer present in the payload.
func handleRobotsMessage(robotsPayload []Robot) {
	// Build a set of robot IDs from the incoming payload.
	newRobotIDs := make(map[int]bool)
	for _, robot := range robotsPayload {
		newRobotIDs[robot.ID] = true

		shouldConnect := false

		// If the robot is not present, mark it for connection.
		if _, exists := robots[robot.ID]; !exists {
			shouldConnect = true
			log.Printf("New robot detected: %+v", robot)
		} else {
			// For existing robots, check if their peer connection is valid.
			pc := robotPeerConnections[robot.ID]
			if pc == nil || pc.state == webrtc.PeerConnectionStateFailed || pc.state == webrtc.PeerConnectionStateClosed {
				shouldConnect = true
				log.Printf("Missing or inactive connection for robot %d", robot.ID)
			} else {
				log.Printf("Robot %d is already connected.", robot.ID)
			}
		}

		// Update the robot record.
		robots[robot.ID] = robot

		// Establish or re-establish connection if required.
		if shouldConnect {
			addRobotPeerConnection(robot.ID)
			if robot.SdpOffer != "" {
				answer := addOfferRobotPeerConnection(robot.ID, robot.SdpOffer)
				SetSdpAnswer(robot.ID, answer)
			}
		}

	}

	// Remove robots that are no longer present in the payload.
	for id := range robots {
		if !newRobotIDs[id] {
			log.Printf("Removing robot %d as it is not present in the new list.", id)
			delete(robots, id)
			// Optionally: close and remove the associated peer connection.
			if pc, exists := robotPeerConnections[id]; exists && pc != nil {
				delete(robotPeerConnections, id)
			}
		}
	}

	NotifyRobotsUpdate()
	log.Println("number of robots:", len(robots))
}

func handleRegisteredMessage(robot Robot) {
	robots[robot.ID] = robot
	addRobotPeerConnection(robot.ID)
	NotifyRobotsUpdate()
	log.Println("Registered robot:", robot)

	if robot.SdpOffer != "" {
		answer := addOfferRobotPeerConnection(robot.ID, robot.SdpOffer)
		SetSdpAnswer(robot.ID, answer)
		log.Println("SDP sending to robot:", robot.ID)
	}
}

func handleOfferMessage(robotID int, sdpOffer string) {
	if robot, exists := robots[robotID]; exists {
		robot.SdpOffer = sdpOffer
		robots[robotID] = robot
		answer := addOfferRobotPeerConnection(robotID, sdpOffer)
		SetSdpAnswer(robotID, answer)
		NotifyRobotsUpdate()
		log.Println("Updated SDP offer for robot:", robotID)
	}
}

func handleAnswerMessage(robotID int, sdpAnswer string) {
	if robot, exists := robots[robotID]; exists {
		robot.SdpAnswer = sdpAnswer
		robots[robotID] = robot
		NotifyRobotsUpdate()
		log.Println("Updated SDP answer for robot:", robotID)
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

func handleDeregisterMessage(robotID int) {
	delete(robots, robotID)
	NotifyRobotsUpdate()
	log.Println("Deregistered robot:", robotID)
	log.Println("Updated robots list:", robots)
}

// GetRobots sends a request to get all registered robots.
func GetRobots() error {
	msg := CommandMessage{Type: "getRobots"}
	return sendMessage(msg)
}

// SetSdpAnswer sets the SDP answer for a specific robot.
func SetSdpAnswer(robotID int, sdpAnswer string) error {
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
