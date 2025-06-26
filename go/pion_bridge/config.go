package bridge

import (
	"embed"
	"errors"
	"log"
	"sync"

	"gopkg.in/yaml.v3"
)

// cfgFS is an embedded filesystem that holds the configuration file.
// The //go:embed directive tells the Go compiler to embed the specified file
// into this variable at compile time.
//
//go:embed config/config.yaml
var cfgFS embed.FS

// AppConfig holds the overall application configuration, parsed from config.yaml.
type AppConfig struct {
	WebRTC WebRTCConfig `yaml:"webrtc"`
}

// WebRTCConfig holds configuration specific to WebRTC peer connections.
type WebRTCConfig struct {
	StunServers []string    `yaml:"stunServers"` // List of STUN server URLs
	TurnServers []ICEServer `yaml:"turnServers"` // List of TURN server configurations
}

// ICEServer represents a TURN server configuration.
type ICEServer struct {
	URLs       []string `yaml:"urls"`                 // URLs for the TURN server
	Username   string   `yaml:"username,omitempty"`   // Username for TURN server authentication (optional)
	Credential string   `yaml:"credential,omitempty"` // Credential (password) for TURN server authentication (optional)
}

var (
	appConfig     *AppConfig // Global variable to store the loaded application configuration.
	loadConfigErr error      // Global variable to store any error encountered during configuration loading.
	loadOnce      sync.Once  // Ensures that the configuration is loaded only once.
)

// embeddedConfigPath is the path to the configuration file within the embedded filesystem.
const embeddedConfigPath = "config/config.yaml"

// GetConfig loads the application configuration from the embedded config.yaml file.
// It ensures that the configuration is loaded only once and returns the loaded configuration
// or an error if loading fails.
func GetConfig() (*AppConfig, error) {
	loadOnce.Do(func() {
		log.Printf("Initializing and loading configuration from: %s", embeddedConfigPath)

		// Read the embedded configuration file.
		data, err := cfgFS.ReadFile(embeddedConfigPath)
		if err != nil {
			loadConfigErr = err
			log.Printf("Error reading embedded configuration file %s: %v", embeddedConfigPath, err)
			return
		}

		// Unmarshal the YAML data into the AppConfig struct.
		var cfg AppConfig
		err = yaml.Unmarshal(data, &cfg)
		if err != nil {
			loadConfigErr = err
			log.Printf("Error unmarshalling YAML configuration: %v", err)
			return
		}

		if len(cfg.WebRTC.StunServers) == 0 {
			loadConfigErr = errors.New("Missing configuration: WebRTC.stunServers")
			return
		}
		// Log a warning if TURN servers are not configured, as they are often crucial for NAT traversal.
		if len(cfg.WebRTC.TurnServers) == 0 {
			log.Println("Warning: WebRTC.turnServers is empty in the configuration.")
		}

		appConfig = &cfg
		log.Println("Configuration loaded successfully.")
	})

	return appConfig, loadConfigErr
}
