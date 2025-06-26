package main

import (
	"fmt"
	"sync"
)

type Encoding int

const (
	XYZ_RGBA_f32          Encoding = iota // 0
	XYZ_RGBA_f32_NaN                      // 1
	XYZ_RGBA_i16                          // 2
	XYZ_RGBA_i16_NaN                      // 3
	XYZ_i16_RGBA_ui8                      // 4
	DIANE_MULTINOMIAL_i16                 // 5
)

var (
	robotPeerConnections      = make(map[int]*PeerConnection)
	robotPeerConnectionsMutex sync.Mutex
)

const DefaultEncoding Encoding = DIANE_MULTINOMIAL_i16

func addRobotPeerConnection(robotID int) {
	pc := NewPeerConnection(robotID, DefaultEncoding)
	pc.Init()
	robotPeerConnectionsMutex.Lock()
	robotPeerConnections[robotID] = pc
	robotPeerConnectionsMutex.Unlock()
}

func addOfferRobotPeerConnection(robotID int, offer string) string {
	pc := robotPeerConnections[robotID]
	if pc == nil {
		fmt.Println("Robot peer connection not found")
		return ""
	}
	answer := pc.AddOffer(offer)
	return answer
}

func AddICECandidateRobotPeerConnection(robotID int, candidate string) {
	pc := robotPeerConnections[robotID]
	if pc == nil {
		fmt.Println("Robot peer connection not found")
		return
	}
	pc.AddIceCandidate(candidate)
}
