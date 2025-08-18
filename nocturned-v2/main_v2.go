package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/usenocturne/nocturned/bluetooth"
	"github.com/usenocturne/nocturned/server"
	"github.com/usenocturne/nocturned/utils"
)

func main() {
	// Command line flags
	var (
		port        = flag.Int("port", 5000, "HTTP server port")
		logFile     = flag.String("log", "", "Log file path (default: stderr)")
		debug       = flag.Bool("debug", false, "Enable debug logging")
		rateLimitRPS = flag.Int("rate-limit-rps", 10, "Rate limit requests per second")
		rateLimitBurst = flag.Int("rate-limit-burst", 5, "Rate limit burst size")
		chunkDelay  = flag.Duration("chunk-delay", 50*time.Millisecond, "Delay between album art chunks")
	)
	flag.Parse()

	// Set up file logging (same as v1 for consistency)
	logDir := "/var/nocturne"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("Warning: Could not create log directory %s: %v", logDir, err)
	}

	// Use provided log file path or default to v2 log
	logPath := *logFile
	if logPath == "" {
		logPath = filepath.Join(logDir, "nocturned_v2.log")
	}

	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("Warning: Could not open log file: %v", err)
		// Continue with stdout logging only
	} else {
		// Log to both file and stdout
		multiWriter := io.MultiWriter(os.Stdout, file)
		log.SetOutput(multiWriter)
		defer file.Close()
		log.Printf("Logging to %s", logPath)
	}

	log.Println("========================================")
	log.Println("Starting Nocturne v2 Service")
	log.Println("========================================")
	log.Printf("Configuration:")
	log.Printf("  Port: %d", *port)
	log.Printf("  Debug: %v", *debug)
	log.Printf("  Log file: %s", logPath)
	log.Printf("  Rate limiting: %d RPS, burst: %d", *rateLimitRPS, *rateLimitBurst)
	log.Printf("  Chunk delay: %v", *chunkDelay)

	// Create WebSocket hub
	log.Println("Initializing WebSocket hub...")
	wsHub := utils.NewWebSocketHub()
	log.Println("WebSocket hub initialized")

	// Create rate limiting configuration
	log.Println("Setting up rate limiting configuration...")
	rateLimitConfig := &bluetooth.RateLimitConfig{
		MaxCommandsPerSecond: *rateLimitRPS,
		BurstSize:           *rateLimitBurst,
		ChunkDelay:          *chunkDelay,
	}

	// Initialize v2 manager
	log.Println("Initializing Bluetooth v2 manager...")
	manager, err := bluetooth.NewManagerV2(wsHub)
	if err != nil {
		log.Fatalf("Failed to create v2 manager: %v", err)
	}
	log.Println("Bluetooth v2 manager initialized successfully")

	// Update rate limiting configuration
	log.Println("Applying rate limiting configuration...")
	manager.UpdateRateLimitConfig(rateLimitConfig)

	// Set up callbacks for demonstration
	log.Println("Setting up BLE event callbacks...")
	manager.SetPlayStateCallback(func(playState *bluetooth.PlayStateMessage) {
		log.Printf("BLE: Play state update - %s by %s (Album: %s)", playState.TrackTitle, playState.Artist, playState.Album)
	})

	manager.SetVolumeCallback(func(volume *bluetooth.VolumeMessage) {
		log.Printf("BLE: Volume update - %d%%", volume.Volume)
	})

	manager.SetWeatherCallback(func(weather *bluetooth.WeatherMessage) {
		log.Printf("BLE: Weather update - %s, %d°C at %s", weather.Condition, weather.Temperature/10, weather.Location)
	})

	// Start the manager
	log.Println("Starting Bluetooth v2 manager...")
	if err := manager.Start(); err != nil {
		log.Fatalf("Failed to start v2 manager: %v", err)
	}
	defer func() {
		log.Println("Stopping Bluetooth v2 manager...")
		manager.Stop()
	}()
	log.Println("Bluetooth v2 manager started successfully")

	// Create and start HTTP server
	log.Printf("Initializing HTTP server on port %d...", *port)
	httpServer := server.NewServerV2(manager, wsHub)
	
	// Start server in a goroutine
	go func() {
		log.Printf("Starting HTTP server on port %d...", *port)
		if err := httpServer.Start(*port); err != nil {
			log.Fatalf("Failed to start HTTP server: %v", err)
		}
	}()
	
	log.Printf("HTTP server started successfully on port %d", *port)
	log.Printf("API endpoints available at: http://localhost:%d/api/v2/", *port)
	log.Printf("WebSocket endpoint available at: ws://localhost:%d/ws", *port)

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	log.Println("========================================")
	log.Println("Nocturne v2 Service is running")
	log.Println("Press Ctrl+C to stop")
	log.Println("========================================")

	// Wait for shutdown signal
	sig := <-sigChan
	log.Printf("Received signal %v, initiating graceful shutdown...", sig)

	log.Println("========================================")
	log.Println("Shutting down Nocturne v2 Service")
	log.Println("========================================")

	// Graceful shutdown
	log.Println("Stopping HTTP server...")
	if err := httpServer.Stop(); err != nil {
		log.Printf("Error stopping HTTP server: %v", err)
	} else {
		log.Println("HTTP server stopped successfully")
	}

	log.Println("Stopping Bluetooth v2 manager...")
	manager.Stop()
	log.Println("Bluetooth v2 manager stopped successfully")

	log.Println("WebSocket hub stopped")

	log.Println("========================================")
	log.Println("Nocturne v2 Service stopped gracefully")
	log.Println("========================================")
}

// Example usage functions for testing

// SendTestPlayState sends a test play state update
func SendTestPlayState(manager *bluetooth.ManagerV2) {
	playState := &bluetooth.PlayStateMessage{
		TrackTitle:   "Test Track",
		Artist:       "Test Artist",
		Album:        "Test Album",
		AlbumArtHash: "testhash123",
		Duration:     180, // 3 minutes
		Position:     60,  // 1 minute
		PlayState:    bluetooth.PlayStatePlaying,
	}

	if err := manager.SendPlayStateUpdate(playState); err != nil {
		log.Printf("Failed to send test play state: %v", err)
	} else {
		log.Println("Test play state sent successfully")
	}
}

// SendTestVolume sends a test volume update
func SendTestVolume(manager *bluetooth.ManagerV2) {
	volume := byte(75) // 75%

	if err := manager.SendVolumeUpdate(volume); err != nil {
		log.Printf("Failed to send test volume: %v", err)
	} else {
		log.Printf("Test volume sent successfully: %d%%", volume)
	}
}

// SendTestWeather sends a test weather update
func SendTestWeather(manager *bluetooth.ManagerV2) {
	weather := &bluetooth.WeatherMessage{
		Temperature: 250, // 25.0°C
		Humidity:    65,  // 65%
		Condition:   "Partly Cloudy",
		Location:    "Test City",
	}

	if err := manager.SendWeatherUpdate(weather); err != nil {
		log.Printf("Failed to send test weather: %v", err)
	} else {
		log.Println("Test weather sent successfully")
	}
}

// Example CLI commands that could be added
func printUsage() {
	fmt.Println("Nocturne v2 Service")
	fmt.Println("Usage: ./main_v2 [options]")
	fmt.Println("")
	fmt.Println("Options:")
	fmt.Println("  -port int")
	fmt.Println("        HTTP server port (default 5000)")
	fmt.Println("  -log string")
	fmt.Println("        Log file path (default: stderr)")
	fmt.Println("  -debug")
	fmt.Println("        Enable debug logging")
	fmt.Println("  -rate-limit-rps int")
	fmt.Println("        Rate limit requests per second (default 10)")
	fmt.Println("  -rate-limit-burst int")
	fmt.Println("        Rate limit burst size (default 5)")
	fmt.Println("  -chunk-delay duration")
	fmt.Println("        Delay between album art chunks (default 50ms)")
	fmt.Println("")
	fmt.Println("API Endpoints:")
	fmt.Println("  GET  /api/v2/status                 - System status")
	fmt.Println("  GET  /api/v2/health                 - Health check")
	fmt.Println("  GET  /api/v2/bluetooth/status       - Bluetooth status")
	fmt.Println("  GET  /api/v2/bluetooth/stats        - Bluetooth statistics")
	fmt.Println("  POST /api/v2/media/play-state       - Send play state")
	fmt.Println("  POST /api/v2/media/volume           - Send volume")
	fmt.Println("  GET  /api/v2/media/current          - Get current media")
	fmt.Println("  POST /api/v2/media/album-art        - Upload album art")
	fmt.Println("  GET  /api/v2/media/album-art/status - Album art status")
	fmt.Println("  GET  /api/v2/media/album-art/cache  - Album art cache")
	fmt.Println("  GET  /api/v2/media/album-art/{hash} - Get album art by hash")
	fmt.Println("  POST /api/v2/weather                - Send weather")
	fmt.Println("  GET  /api/v2/weather/current        - Get current weather")
	fmt.Println("  GET  /ws                            - WebSocket endpoint")
}