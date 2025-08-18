package utils

import (
	//"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type WebSocketHub struct {
	clients map[*websocket.Conn]bool
	mu      sync.Mutex
}

func NewWebSocketHub() *WebSocketHub {
	return &WebSocketHub{
		clients: make(map[*websocket.Conn]bool),
	}
}

func (h *WebSocketHub) AddClient(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[conn] = true
}

func (h *WebSocketHub) RemoveClient(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[conn]; ok {
		delete(h.clients, conn)
		conn.Close()
	}
}

func (h *WebSocketHub) Broadcast(event WebSocketEvent) {
	h.mu.Lock()
	// Create a snapshot of current clients
	clients := make([]*websocket.Conn, 0, len(h.clients))
	for conn := range h.clients {
		clients = append(clients, conn)
	}
	h.mu.Unlock()

	// Send to each client asynchronously
	var wg sync.WaitGroup
	var failedClients []*websocket.Conn
	var failedMu sync.Mutex

	for _, conn := range clients {
		wg.Add(1)
		go func(c *websocket.Conn) {
			defer wg.Done()
			
			// Set write deadline to prevent slow clients from blocking too long
			c.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
			if err := c.WriteJSON(event); err != nil {
				failedMu.Lock()
				failedClients = append(failedClients, c)
				failedMu.Unlock()
			}
		}(conn)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Remove failed clients
	if len(failedClients) > 0 {
		h.mu.Lock()
		for _, conn := range failedClients {
			if _, ok := h.clients[conn]; ok {
				delete(h.clients, conn)
				conn.Close()
			}
		}
		h.mu.Unlock()
	}
}
