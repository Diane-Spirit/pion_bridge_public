package bridge

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beevik/ntp"
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/cc"
	"github.com/pion/interceptor/pkg/nack"
	"github.com/pion/interceptor/pkg/twcc"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v3"
)

type PeerConnectionFrame struct {
	ClientID   uint64 // Identifier for the client associated with this frame.
	FrameNr    uint32 // Sequence number of the frame.
	FrameLen   uint32 // Total expected length of the frame.
	CurrentLen uint32 // Current accumulated length of the frame.
	FrameData  []byte // Buffer to store the frame data.
}

// NewPeerConnectionFrame creates and initializes a new PeerConnectionFrame.
func NewPeerConnectionFrame(clientID uint64, frameNr uint32, frameLen uint32) *PeerConnectionFrame {
	return &PeerConnectionFrame{
		ClientID:   clientID,
		FrameNr:    frameNr,
		FrameLen:   frameLen,
		CurrentLen: 0,
		FrameData:  make([]byte, frameLen),
	}
}

func (pf *PeerConnectionFrame) IsComplete() bool {
	return pf.CurrentLen == pf.FrameLen
}

// PeerConnection manages a single WebRTC peer connection with a robot.
// It handles ICE, SDP, data channels, and media tracks.
type PeerConnection struct {
	webrtcConnection *webrtc.PeerConnection // The underlying WebRTC peer connection object.
	robotID          int                    // Identifier for the robot this connection is with.
	encoding         Encoding               // The encoding type used for media.
	candidatesMux    sync.Mutex             // Mutex to protect access to pendingCandidates.
	// pendingCandidates stores ICE candidates that were gathered before the remote description was set.
	pendingCandidates []*webrtc.ICECandidate
	estimator         cc.BandwidthEstimator      // For bandwidth estimation (if used).
	api               *webrtc.API                // The WebRTC API instance with configured interceptors and media engine.
	state             webrtc.PeerConnectionState // Current state of the peer connection.
	bandwidthDC       *webrtc.DataChannel        // Data channel used for sending bandwidth estimation information.
	controltrackDC    *webrtc.DataChannel        // Data channel for control messages related to tracks.

	// completedFramesChannel is a ring buffer for storing fully received frames from the OnTrack callback.
	completedFramesChannel *RingChannel
	dataChannel            *webrtc.DataChannel // Generic data channel for application-specific data.
	testRoutineRunning     bool                // Flag indicating if the test data sending routine is active.
	testRoutineStopChan    chan struct{}       // Channel to signal the test routine to stop.
}

// NewPeerConnection creates a new PeerConnection instance for a given robot ID and encoding.
// It initializes the WebRTC API with necessary interceptors and media engine settings.
func NewPeerConnection(robotID int, encoding Encoding) *PeerConnection {
	api := InitApi() // Initialize the WebRTC API.
	pc := &PeerConnection{
		robotID:                robotID,
		encoding:               encoding,
		candidatesMux:          sync.Mutex{},
		pendingCandidates:      make([]*webrtc.ICECandidate, 0),
		api:                    api,
		completedFramesChannel: NewRingChannel(5),
	}
	log.Printf("New PeerConnection created for robot ID: %d", robotID)
	return pc
}

// Init initializes the WebRTC peer connection, sets up ICE servers,
// registers callbacks for various events (data channels, ICE candidates, connection state, tracks),
// and creates necessary data channels.
func (pc *PeerConnection) Init() {
	log.Printf("Initializing PeerConnection for robot ID: %d", pc.robotID)

	cfg, err := GetConfig()
	if err != nil {
		log.Fatalf("Failed to get config: %v", err)
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

	fmt.Println(config)

	pc.webrtcConnection, err = pc.api.NewPeerConnection(config)
	if err != nil {
		log.Fatalf("Failed to create PeerConnection for robot ID %d: %v", pc.robotID, err)
		// In a real application, avoid panic; return an error instead.
	}
	log.Printf("WebRTC PeerConnection object created for robot ID: %d", pc.robotID)

	// Register callbacks for various WebRTC events.
	pc.webrtcConnection.OnDataChannel(pc.OnDataChannelCb)
	pc.webrtcConnection.OnICECandidate(pc.OnIceCandidateCb)
	pc.webrtcConnection.OnConnectionStateChange(pc.OnConnectionStateChangeCb)
	pc.webrtcConnection.OnTrack(pc.OnTrackCb)
	log.Printf("Callbacks registered for PeerConnection robot ID: %d", pc.robotID)

	// Create a data channel for bandwidth information.
	pc.bandwidthDC, err = pc.webrtcConnection.CreateDataChannel("bandwidth", nil)
	if err != nil {
		log.Fatalf("Failed to create 'bandwidth' data channel for robot ID %d: %v", pc.robotID, err)
	}
	pc.bandwidthDC.OnOpen(pc.onOpenCb)                               // Callback for when the channel opens.
	pc.bandwidthDC.OnMessage(func(msg webrtc.DataChannelMessage) {}) // No-op message handler for now.
	pc.bandwidthDC.OnClose(func() {                                  // Callback for when the channel closes.
		log.Printf("'bandwidth' data channel closed for robot ID: %d", pc.robotID)
	})
	log.Printf("'bandwidth' data channel created for robot ID: %d", pc.robotID)

	// Create a data channel for control track information.
	ordered := true
	maxRetransmits := uint16(30)
	pc.controltrackDC, err = pc.webrtcConnection.CreateDataChannel("controltrack", &webrtc.DataChannelInit{
		Ordered:        &ordered,        // Ensure messages are delivered in order.
		MaxRetransmits: &maxRetransmits, // Limit the number of retransmissions.
	})
	if err != nil {
		log.Fatalf("Failed to create 'controltrack' data channel for robot ID %d: %v", pc.robotID, err)
	}
	// Note: Callbacks for controltrackDC (OnOpen, OnMessage, OnClose) should also be set up if needed.
	log.Printf("'controltrack' data channel created for robot ID: %d", pc.robotID)
}

// onOpenCb is a callback function triggered when a data channel opens.
// This specific instance is used for the 'bandwidth' data channel.
func (pc *PeerConnection) onOpenCb() {
	log.Printf("Data channel 'bandwidth' has been opened for robot ID: %d", pc.robotID)
}

// OnDataChannelCb is a callback function triggered when a new data channel is established by the remote peer.
func (pc *PeerConnection) OnDataChannelCb(dc *webrtc.DataChannel) {
	log.Printf("New DataChannel '%s' (ID: %d) received for robot ID: %d", dc.Label(), *dc.ID(), pc.robotID)

	pc.dataChannel = dc // Store the reference to the new data channel.

	// Register channel opening handling.
	dc.OnOpen(func() {
		log.Printf("Data channel '%s' (ID: %d) opened for robot ID: %d", dc.Label(), *dc.ID(), pc.robotID)
	})

	// Register text message handling.
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		log.Printf("Message from DataChannel '%s' (ID: %d) for robot ID %d: \"%s\"", dc.Label(), *dc.ID(), pc.robotID, string(msg.Data))
	})

	// Register channel closing handling.
	dc.OnClose(func() {
		log.Printf("Data channel '%s' (ID: %d) closed for robot ID: %d", dc.Label(), *dc.ID(), pc.robotID)
		if pc.dataChannel == dc {
			pc.dataChannel = nil // Clear the reference if this was the stored data channel.
		}
	})
}

// OnTrackCb is a callback function triggered when a new remote media track is received.
// It handles reading RTP packets from the track, reassembling frames, and processing them.
func (pc *PeerConnection) OnTrackCb(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
	log.Printf("OnTrack called for robot ID: %d. Track Kind: %s, Codec: %s, PayloadType: %d, SSRC: %d",
		pc.robotID, track.Kind(), track.Codec().MimeType, track.PayloadType(), track.SSRC())

	// Prepare CSV logging for track data.
	timestampFormat := time.Now().UTC().Format("20060102_150405")
	filePath := fmt.Sprintf("/storage/emulated/0/Documents/log_go_metaquest%d_%s.csv", pc.robotID, timestampFormat)
	header := []string{"Timestamp", "WebSocketTime(us)", "nopReceived", "nopOriginal", "lossRate"}
	csvFile, err := NewManagedCSV(filePath, header, true)
	if err != nil {
		log.Printf("Error initializing managed CSV for AddDataRecord: %v", err)
		return
	}

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
	var lossRate float64

	timeFromNTP, err := ntp.Time("pool.ntp.org") // puoi mettere il tuo server NTP
	if err != nil {
		panic(err)
	}
	offset := timeFromNTP.Sub(time.Now())

	startTime := time.Now()
	for {

		currentSelectedRobotId := GetSelectedRobotId()
		if currentSelectedRobotId != pc.robotID {
			_, _, readErr := track.Read(buf)
			if readErr != nil {
				panic(readErr)
			}
			time.Sleep(100 * time.Millisecond) // Sleep briefly to avoid busy-waiting.
			continue
		}

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
			timestamp := float64(time.Now().Add(offset).UnixNano()) / 1e9

			totalLen := len(coordinateBuffer) + len(colorBuffer)

			nopReceived := int64(packet_recv * 150)
			nopOriginal := int64((currentFrameTotalLength / 8))

			if nopReceived > nopOriginal {
				nopReceived = nopOriginal
			}

			if nopOriginal > 0 {
				lossRate = 1 - (float64(nopReceived) / float64(nopOriginal))
			} else {
				lossRate = 0
			}

			// reorganizedBuffer := make([]byte, 0, totalLen)
			reorganizedBuffer = reorganizedBuffer[:0]
			reorganizedBuffer = append(reorganizedBuffer, coordinateBuffer[:nopReceived*6]...)
			reorganizedBuffer = append(reorganizedBuffer, colorBuffer[:nopReceived*2]...)

			bw := int32((float64(totalLen) / deltaTime.Seconds()) * 8)
			if bw < 3779 {
				bw = 3779
			}

			var buf bytes.Buffer
			if err := binary.Write(&buf, binary.LittleEndian, bw); err != nil {
				fmt.Println("Errore durante la scrittura:", err)
				return
			}

			pc.bandwidthDC.Send(buf.Bytes())

			startTimeSendFrame := time.Now()
			SendFrameToClients(reorganizedBuffer[:])
			durationSendFrame := time.Since(startTimeSendFrame)
			old_frame = p.FrameNr

			csvFile.AppendData([]string{
				strconv.FormatFloat(timestamp, 'f', 10, 64),
				strconv.FormatInt(durationSendFrame.Microseconds(), 10),
				strconv.FormatInt(nopReceived, 10),
				strconv.FormatInt(nopOriginal, 10),
				strconv.FormatFloat(lossRate, 'f', 4, 64), // 'f' for format, 4 decimal places, 64-bit float
			})

			currentFrameTotalLength = p.FrameLen

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
			log.Printf("Warning: Received packet with unexpected SeqLen %d for DIANE_MULTINOMIAL_i16 encoding for robot %d. Expected %d.",
				p.SeqLen, pc.robotID, 150*8)
		}
		break
	default:
		log.Fatalf("Unsupported encoding type in appendMessageBuffer for robot ID %d: %v", pc.robotID, pc.encoding)
	}
}

// OnIceCandidateCb is a callback function triggered when a new local ICE candidate is gathered.
// It sends the candidate to the remote peer via the signaling server.
func (pc *PeerConnection) OnIceCandidateCb(c *webrtc.ICECandidate) {
	if c == nil {
		log.Printf("Nil ICE candidate received for robot ID: %d. Ignoring.", pc.robotID)
		return
	}
	log.Printf("Local ICE candidate gathered for robot ID %d: %s", pc.robotID, c.ToJSON().Candidate)

	pc.candidatesMux.Lock()
	defer pc.candidatesMux.Unlock()

	desc := pc.webrtcConnection.RemoteDescription()
	if desc == nil {
		// If remote description is not yet set, queue the candidate.
		log.Printf("Remote description not set for robot ID %d. Queuing ICE candidate.", pc.robotID)
		pc.pendingCandidates = append(pc.pendingCandidates, c)
	} else {
		// Remote description is set, send the candidate immediately.
		// AddIceCandidate is assumed to be a function in the signaling_client.go that sends it.
		// It expects a string representation of the candidate.
		err := AddIceCandidate(pc.robotID, c.ToJSON().Candidate)
		if err != nil {
			log.Printf("Error sending ICE candidate for robot ID %d: %v", pc.robotID, err)
		}
	}
}

// OnConnectionStateChangeCb is a callback function triggered when the peer connection state changes.
func (pc *PeerConnection) OnConnectionStateChangeCb(s webrtc.PeerConnectionState) {
	fmt.Printf("Peer connection state has changed: %s\n", s.String())
	if s == webrtc.PeerConnectionStateFailed {
		fmt.Println("Peer connection has gone to failed exiting")
	}
	pc.state = s
}

func InitApi() *webrtc.API {
	log.Println("Initializing WebRTC API with custom settings.")
	settingEngine := webrtc.SettingEngine{}
	i := &interceptor.Registry{}
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		panic(err)
	}

	// Define RTCP feedback for a custom video codec.
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

// AddOffer sets the remote SDP offer on the peer connection and creates an SDP answer.
// It then sets the local description to this answer and returns the answer's SDP.
func (pc *PeerConnection) AddOffer(offerString string) string {
	log.Printf("Processing SDP offer for robot ID %d.", pc.robotID)
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offerString,
	}

	if err := pc.webrtcConnection.SetRemoteDescription(offer); err != nil {
		log.Fatalf("Failed to set remote description for robot ID %d: %v", pc.robotID, err)
	}
	log.Printf("Remote description (offer) set for robot ID %d.", pc.robotID)

	// Create an SDP answer.
	answer, err := pc.webrtcConnection.CreateAnswer(nil)
	if err != nil {
		log.Fatalf("Failed to create answer for robot ID %d: %v", pc.robotID, err)
	}
	log.Printf("SDP answer created for robot ID %d.", pc.robotID)

	// Set the local description to the created answer.
	if err = pc.webrtcConnection.SetLocalDescription(answer); err != nil {
		panic(err)
	}

	return answer.SDP
}

// AddIceCandidate adds a remote ICE candidate to the peer connection.
func (pc *PeerConnection) AddIceCandidate(candidate string) {
	log.Printf("Adding remote ICE candidate for robot ID %d: %s", pc.robotID, candidate)
	// The candidate string should be in the format expected by AddICECandidate.
	// webrtc.ICECandidateInit{Candidate: candidate} correctly wraps it.
	if err := pc.webrtcConnection.AddICECandidate(webrtc.ICECandidateInit{Candidate: candidate}); err != nil {
		// Log error but don't panic, as adding a candidate can sometimes fail legitimately.
		log.Printf("Error adding remote ICE candidate for robot ID %d: %v", pc.robotID, err)
	} else {
		log.Printf("Successfully added remote ICE candidate for robot ID %d.", pc.robotID)
	}
}
