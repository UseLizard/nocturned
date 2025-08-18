package server

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/usenocturne/nocturned/bluetooth"
	"github.com/usenocturne/nocturned/utils"
)

// Server holds the dependencies for the HTTP server.
 type Server struct {
	btManager *bluetooth.BluetoothManager
	wsHub     *utils.WebSocketHub
	router    *http.ServeMux
}

// NewServer creates a new Server instance.
func NewServer(btManager *bluetooth.BluetoothManager, wsHub *utils.WebSocketHub) *Server {
	s := &Server{
		btManager: btManager,
		wsHub:     wsHub,
		router:    http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

// Start starts the HTTP server.
func (s *Server) Start() {
	server := &http.Server{
		Addr:    ":8080",
		Handler: s.router,
	}

	go func() {
		log.Println("Starting server on :8080")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Could not start server: %v", err)
		}
	}()

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down server...")

	// Create a deadline to wait for.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server shutdown failed: %v", err)
	}

	log.Println("Server gracefully stopped")
}