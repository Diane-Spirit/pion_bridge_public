package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/cc"
	"github.com/pion/interceptor/pkg/nack"
	"github.com/pion/interceptor/pkg/twcc"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v3"
)

type PeerConnectionFrame struct {
	ClientID   uint64
	FrameNr    uint32
	FrameLen   uint32
	CurrentLen uint32
	FrameData  []byte
}

func NewPeerConnectionFrame(clientID uint64, frameNr uint32, frameLen uint32) *PeerConnectionFrame {
	return &PeerConnectionFrame{clientID, frameNr, frameLen, 0, make([]byte, frameLen)}
}

func (pf *PeerConnectionFrame) IsComplete() bool {
	return pf.CurrentLen == pf.FrameLen
}

type PeerConnection struct {
	webrtcConnection  *webrtc.PeerConnection
	robotID           int
	encoding          Encoding
	candidatesMux     sync.Mutex
	pendingCandidates []*webrtc.ICECandidate
	estimator         cc.BandwidthEstimator
	api               *webrtc.API
	state             webrtc.PeerConnectionState
	bandwidthDC       *webrtc.DataChannel
	controltrackDC    *webrtc.DataChannel

	completedFramesChannel *RingChannel
	dataChannel            *webrtc.DataChannel
	testRoutineRunning     bool
	testRoutineStopChan    chan struct{}
}

func NewPeerConnection(robotID int, encoding Encoding) *PeerConnection {
	api := InitApi()
	pc := &PeerConnection{
		robotID:                robotID,
		encoding:               encoding,
		candidatesMux:          sync.Mutex{},
		pendingCandidates:      make([]*webrtc.ICECandidate, 0),
		api:                    api,
		completedFramesChannel: NewRingChannel(5),
	}
	return pc
}

func (pc *PeerConnection) Init() {

	cfg, err := GetConfig()
	if err != nil {
		fmt.Printf("Failed to get config: %v", err)
	}
	rtcCfg := cfg.WebRTC

	var iceServers []webrtc.ICEServer
	for _, stunURL := range rtcCfg.StunServers {
		iceServers = append(iceServers, webrtc.ICEServer{URLs: []string{stunURL}})
	}
	for _, turnCfg := range rtcCfg.TurnServers {
		iceServers = append(iceServers, webrtc.ICEServer{
			URLs:       turnCfg.URLs,
			Username:   turnCfg.Username,
			Credential: turnCfg.Credential,
		})
	}

	config := webrtc.Configuration{ICEServers: iceServers}

	pc.webrtcConnection, err = pc.api.NewPeerConnection(config)
	if err != nil {
		fmt.Printf("Failed to create PeerConnection for robot ID %d: %v", pc.robotID, err)
		// In a real application, avoid panic; return an error instead.
	}

	// ------------------ Callbacks ------------------
	pc.webrtcConnection.OnDataChannel(pc.OnDataChannelCb)
	pc.webrtcConnection.OnICECandidate(pc.OnIceCandidateCb)
	pc.webrtcConnection.OnConnectionStateChange(pc.OnConnectionStateChangeCb)
	pc.webrtcConnection.OnTrack(pc.OnTrackCb)

	// ------------------ DataChannel ------------------
	bandwidthDC, err := pc.webrtcConnection.CreateDataChannel("bandwidth", nil)

	ordered := true
	MaxRetransmits := uint16(30)
	controltrackDC, err := pc.webrtcConnection.CreateDataChannel("controltrack", &webrtc.DataChannelInit{
		Ordered:        &ordered,
		MaxRetransmits: &MaxRetransmits,
	})
	if err != nil {
		panic(err)
	}
	controltrackDC.OnOpen(func() {
		controltrackDC.Send([]byte("start"))
	})
	bandwidthDC.OnOpen(pc.onOpenCb)
	bandwidthDC.OnMessage(func(msg webrtc.DataChannelMessage) {})
	bandwidthDC.OnClose(func() {})

	pc.bandwidthDC = bandwidthDC
	pc.controltrackDC = controltrackDC
}

func (pc *PeerConnection) onOpenCb() {
	fmt.Println("Data channel has been opened")
}

func (pc *PeerConnection) OnDataChannelCb(dc *webrtc.DataChannel) {
	fmt.Printf("New DataChannel %q %d\n", dc.Label(), dc.ID())

	pc.dataChannel = dc

	// Register channel opening handling
	dc.OnOpen(func() {
		fmt.Printf("Data channel %q %d opened\n", dc.Label(), dc.ID())

		fmt.Println("Data channel is now open and ready for communication.")
		pc.StartTestRoutine()
	})

	// Register text message handling
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		fmt.Printf("Message from DataChannel %q %d: \"%s\"\n", dc.Label(), dc.ID(), string(msg.Data))
	})

	// Register channel closing handling
	dc.OnClose(func() {
		fmt.Printf("Data channel %q %d closed\n", dc.Label(), dc.ID())
		pc.dataChannel = nil
	})
}

func (pc *PeerConnection) OnTrackCb(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {

	println("OnTrack has been called")
	println("MIME type:", track.Codec().MimeType)
	println("Payload type:", track.PayloadType())

	codecName := strings.Split(track.Codec().RTPCodecCapability.MimeType, "/")
	fmt.Printf("Track of type %d has started: %s \n", track.PayloadType(), codecName)

	buf := make([]byte, len(FramePacket{}.Data)+40)

	var old_frame uint32 = 0

	reorganizedBuffer := make([]byte, 0, 480*480*8)

	coordinateBuffer := make([]byte, 0, 480*480*6)
	colorBuffer := make([]byte, 0, 480*480*2)

	packet_recv := 0
	var currentFrameTotalLength uint32 = 0

	startTime := time.Now()
	for {
		_, _, readErr := track.Read(buf)
		if readErr != nil {
			panic(readErr)
		}
		bufBinary := bytes.NewBuffer(buf[20:])
		var p FramePacket
		err := binary.Read(bufBinary, binary.LittleEndian, &p)

		if currentFrameTotalLength == 0 {
			currentFrameTotalLength = p.FrameLen
		}

		if p.FrameNr != old_frame {

			deltaTime := time.Now().Sub(startTime)

			nopReceived := int64(packet_recv * 150)
			nopOriginal := int64((currentFrameTotalLength / 8))

			if nopReceived > nopOriginal {
				nopReceived = nopOriginal
			}

			totalLen := len(coordinateBuffer) + len(colorBuffer)
			// reorganizedBuffer := make([]byte, 0, totalLen)
			reorganizedBuffer = reorganizedBuffer[:0]
			reorganizedBuffer = append(reorganizedBuffer, coordinateBuffer[:nopReceived*6]...)
			reorganizedBuffer = append(reorganizedBuffer, colorBuffer[:nopReceived*2]...)

			bw := int32((float64(totalLen) / deltaTime.Seconds()) * 8)
			if bw < 3779 {
				bw = 3779
			}
			fmt.Println("Bandwidth:", bw)

			var buf bytes.Buffer
			if err := binary.Write(&buf, binary.LittleEndian, bw); err != nil {
				fmt.Println("Errore durante la scrittura:", err)
				return
			}

			pc.bandwidthDC.Send(buf.Bytes())
			SendFrameToClients(reorganizedBuffer[:])
			old_frame = p.FrameNr

			coordinateBuffer = coordinateBuffer[:0]
			colorBuffer = colorBuffer[:0]

			pc.appendMessageBuffer(&coordinateBuffer, &colorBuffer, &p)
			packet_recv = 1

			startTime = time.Now()
		} else {
			pc.appendMessageBuffer(&coordinateBuffer, &colorBuffer, &p)
			packet_recv++
		}

		if err != nil {
			panic(err)
		}
	}
}

func (pc *PeerConnection) appendMessageBuffer(coordinateBuffer *[]byte, colorBuffer *[]byte, p *FramePacket) {
	switch pc.encoding {
	case DIANE_MULTINOMIAL_i16:
		if p.SeqLen == 150*8 {
			element_size := uint32(p.SeqLen / 4)
			*coordinateBuffer = append(*coordinateBuffer, p.Data[:3*element_size]...)
			*colorBuffer = append(*colorBuffer, p.Data[3*element_size:p.SeqLen]...)
		} else {
			fmt.Println("Dimensione del pacchetto non valida:", p.SeqLen)
		}
		break
	default:
		panic("Encoding not supported")
	}

}

func (pc *PeerConnection) OnIceCandidateCb(c *webrtc.ICECandidate) {
	if c == nil {
		return
	}
	pc.candidatesMux.Lock()
	desc := pc.webrtcConnection.RemoteDescription()
	if desc == nil {
		pc.pendingCandidates = append(pc.pendingCandidates, c)
	} else {
		AddIceCandidate(pc.robotID, c.ToJSON().Candidate)
	}
	pc.candidatesMux.Unlock()
}

// TODO Change implentation => add connection to completed clients
func (pc *PeerConnection) OnConnectionStateChangeCb(s webrtc.PeerConnectionState) {
	fmt.Printf("Peer connection state has changed: %s\n", s.String())
	if s == webrtc.PeerConnectionStateFailed {
		fmt.Println("Peer connection has gone to failed exiting")
	}
	pc.state = s
}

func InitApi() *webrtc.API {

	settingEngine := webrtc.SettingEngine{}
	i := &interceptor.Registry{}
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		panic(err)
	}
	videoRTCPFeedback := []webrtc.RTCPFeedback{
		{Type: "goog-remb", Parameter: ""},
		{Type: "ccm", Parameter: "fir"},
		{Type: "nack", Parameter: ""},
		{Type: "nack", Parameter: "pli"},
	}

	codecCapability := webrtc.RTPCodecCapability{
		MimeType:     "video/pcm",
		ClockRate:    90000,
		Channels:     0,
		SDPFmtpLine:  "",
		RTCPFeedback: videoRTCPFeedback,
	}

	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: codecCapability,
		PayloadType:        5,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		panic(err)
	}

	m.RegisterFeedback(webrtc.RTCPFeedback{Type: "nack"}, webrtc.RTPCodecTypeVideo)
	m.RegisterFeedback(webrtc.RTCPFeedback{Type: "nack", Parameter: "pli"}, webrtc.RTPCodecTypeVideo)

	if err := webrtc.ConfigureTWCCHeaderExtensionSender(m, i); err != nil {
		panic(err)
	}

	responder, _ := nack.NewResponderInterceptor()
	i.Add(responder)

	m.RegisterFeedback(webrtc.RTCPFeedback{Type: webrtc.TypeRTCPFBTransportCC}, webrtc.RTPCodecTypeVideo)
	if err := m.RegisterHeaderExtension(webrtc.RTPHeaderExtensionCapability{URI: sdp.TransportCCURI}, webrtc.RTPCodecTypeVideo); err != nil {
		panic(err)
	}

	m.RegisterFeedback(webrtc.RTCPFeedback{Type: webrtc.TypeRTCPFBTransportCC}, webrtc.RTPCodecTypeAudio)
	if err := m.RegisterHeaderExtension(webrtc.RTPHeaderExtensionCapability{URI: sdp.TransportCCURI}, webrtc.RTPCodecTypeAudio); err != nil {
		panic(err)
	}

	generator, err := twcc.NewSenderInterceptor(twcc.SendInterval(1 * time.Millisecond))
	if err != nil {
		panic(err)
	}

	i.Add(generator)

	nackGenerator, _ := nack.NewGeneratorInterceptor()
	i.Add(nackGenerator)

	api := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine), webrtc.WithInterceptorRegistry(i), webrtc.WithMediaEngine(m))

	return api
}

func (pc *PeerConnection) AddOffer(offerString string) string {
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offerString,
	}

	pc.webrtcConnection.SetRemoteDescription(offer)
	answer, err := pc.webrtcConnection.CreateAnswer(nil)
	if err != nil {
		panic(err)
	}
	if err = pc.webrtcConnection.SetLocalDescription(answer); err != nil {
		panic(err)
	}

	return answer.SDP
}

func (pc *PeerConnection) AddIceCandidate(candidate string) {
	if candidateErr := pc.webrtcConnection.AddICECandidate(webrtc.ICECandidateInit{Candidate: candidate}); candidateErr != nil {
		panic(candidateErr)
	}
}

// StartTestRoutine starts a goroutine that sends test JSON data every 2 seconds.
func (pc *PeerConnection) StartTestRoutine() {
	if pc.testRoutineRunning {
		fmt.Println("Test routine is already running.")
		return
	}
	if pc.dataChannel == nil {
		fmt.Println("Data channel is not yet available. Test routine will not start.")
		return
	}
	if pc.dataChannel.ReadyState() != webrtc.DataChannelStateOpen {
		fmt.Println("Data channel is not open. Test routine will not start.")
		return
	}

	pc.testRoutineRunning = true
	fmt.Println("Starting test routine to send data every 2 seconds.")

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		i := 0.0

		for {
			select {
			case <-ticker.C:
				if pc.dataChannel == nil {
					continue
				}
				data := map[string]interface{}{
					"linear": map[string]float64{
						"x": 192.0 + i,
						"y": 2300.0 + i,
						"z": 123.0 + i,
					},
					"angular": map[string]float64{
						"x": 192.0 + i,
						"y": 2300.0 + i,
						"z": 123.0 + i,
					},
				}
				jsonData, err := json.Marshal(data)
				if err != nil {
					fmt.Println("Error marshaling JSON:", err)
					continue
				}

				err = pc.dataChannel.Send(jsonData)
				if err != nil {
					fmt.Println("Error sending data channel message:", err)
					// Optionally stop the routine if sending fails persistently
					// pc.StopTestRoutine()
					// return
				} else {
					fmt.Println("Sent:", string(jsonData))
				}

				i++

			case <-pc.testRoutineStopChan:
				fmt.Println("Stopping test routine.")
				pc.testRoutineRunning = false
				return
			}
		}
	}()
}

// StopTestRoutine stops the test data sending goroutine.
func (pc *PeerConnection) StopTestRoutine() {
	if pc.testRoutineRunning {
		close(pc.testRoutineStopChan)
		// Create a new channel for the next run if needed
		pc.testRoutineStopChan = make(chan struct{})
		pc.testRoutineRunning = false
	}
}
