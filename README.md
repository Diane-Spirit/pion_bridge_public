# Pion Bridge

A framework to forward WebRTC streams from Android to a local WebSocket server.  
It allows easy integration of Go logic with an Android app via GoMobile, or to run standalone tests in Docker.

---

## Table of Contents

- [Introduction](#introduction)  
- [Prerequisites](#prerequisites)  
- [Project Structure](#project-structure)  
- [Android Build](#android-build)  
- [Running Standalone Go Tests](#running-standalone-go-tests)  
- [Docker Build](#docker-build)  
- [Configuration](#configuration)  
- [Usage](#usage)  

---

## Introduction

Pion Bridge provides:
- An Android service (`app/`) that starts an internal WebRTC→WebSocket bridge  
- A Go library (`go/pion_bridge/`) compiled with [`gomobile`](https://pkg.go.dev/golang.org/x/mobile/cmd/gomobile)  
- A standalone test project (`go/pion_bridge_test/`) to validate Go logic without the app  

---

## Prerequisites

- Go ≥ 1.24
- gomobile (`go install golang.org/x/mobile/cmd/gomobile@latest`)  
- Android SDK & NDK (for AAR build)  
- Docker (optional, for containerized builds)  

---

## Project Structure

The main project structure is as follows:

```  
pion_bridge/  
├── app/                     # Android application code  
│   ├── src/  
│   │   ├── main/  
│   │   │   ├── java/  
│   │   │   │   └── com/  
│   │   │   │       └── diane/  
│   │   │   │           └── pionbridge/  
│   │   │   │               ├── BridgeService.kt  # Android service managing the bridge  
│   │   │   │               ├── MainActivity.kt   # Main activity of the app  
│   │   │   ├── AndroidManifest.xml  
│   │   │   └── res/  
│   └── build.gradle  
├── go/                       # Go code for bridge logic and tests  
│   ├── pion_bridge/         # Go code for the bridge used by the Android app  
│   │   ├── config/          # config.yaml embedded  
│   │   └── *.go             # Other Go source files (e.g., bridge.go, ws_server.go)  
│   ├── pion_bridge_test/    # Standalone Go tests (without Android app)  
│   │   ├── config/          # config.yaml for tests  
│   │   └── *.go             # Test mocks, WebSocket client, etc.  
│   ├── build_pion.sh        # Script to build AAR with gomobile  
│   ├── run_go.sh            # Script to build and run tests via Docker  
│   └── Dockerfile           # Container context for build/test  
├── go.mod  
└── README.md                # This file  
```

---

## Android Build

Before compiling the Go code, make sure you have:

1. **Go**: Installed on your system. Download from [golang.org](https://golang.org/dl/).  
2. **Gomobile** (for Android build via script): If you intend to use `build_pion.sh`, install [gomobile](https://pkg.go.dev/golang.org/x/mobile/cmd/gomobile).  

3. **Set Up Go Modules**:  
   ```bash
   cd go
   go mod tidy
   gomobile init
   ```

4. **Build the Go Libraries**:  
   ```bash
   ./build_pion.sh
   ```

5. Import `bridge.aar` in `app/build.gradle` to include the Go library in the Android project.

---

## Running Standalone Go Tests

To run the Go test code without the Android app and web UI:

```bash
cd go
./run_go.sh
```

This script uses Docker to build and execute the tests in `go/pion_bridge_test/`.

---

## Docker Build

To generate the AAR inside an isolated container:

```bash
docker build -t gomobandroid:latest go/
docker run --rm \
  -v "$PWD/go":/module \
  -v "$PWD/libs":/output \
  gomobandroid:latest \
    bind -target=android -androidapi 31 \
    -ldflags="-checklinkname=0" \
    -o /output/bridge.aar ./pion_bridge
```

---

## Configuration

The YAML files are **embedded** into the Go code at compile time. After any modification to a `config.yaml`, recompile with `build_pion.sh` or `run_go.sh`.

### go/pion_bridge/config/config.yaml

```yaml
# ./go/pion_bridge/config/config.yaml
webrtc:
  stunServers:
    - "stun:stun.l.google.com:19302"
  turnServers:
    - urls:
        - "turn:your.turn.server:3478"
      username: "username"
      credential: "credential"
```

### go/pion_bridge_test/config/config.yaml

```yaml
# ./go/pion_bridge_test/config/config.yaml
websocket:
  address: "your.server.address:3001"
  retryInterval: 1s

webrtc:
  stunServers:
    - "stun:stun.l.google.com:19302"
  turnServers:
    - urls:
        - "turn:your.turn.server:3478"
      username: "username"
      credential: "credential"
```

---

## Usage

1. **Start the Android Service**: Launch the `BridgeService` from the Android app to start the WebRTC to WebSocket bridge.  
2. **Connect to WebSocket**: Connect your web application to the local WebSocket server at `ws://localhost:8080`.  
3. **Stop the Service**: Use the provided UI in the Android app to stop the service when not needed.  

---

