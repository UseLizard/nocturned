package bluetooth

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/godbus/dbus/v5"
)

func (c *BleClient) handleBleNotifications() {
	// Add panic recovery for this critical goroutine
	defer func() {
		if r := recover(); r != nil {
			log.Printf("BLE_LOG: PANIC in handleBleNotifications: %v - attempting to reconnect", r)
			c.handleConnectionLoss()
		}
	}()

	log.Println("BLE_LOG: handleBleNotifications goroutine started")

	// Start monitoring notifications
	go c.monitorCharacteristicNotifications()

	// Keep the goroutine alive and monitor connection
	ticker := time.NewTicker(60 * time.Second) // Increased from 30s to reduce connection checks
	defer ticker.Stop()

	connectionCheckFailures := 0

	for {
		select {
		case <-c.stopChan:
			log.Println("BLE_LOG: handleBleNotifications stopping")
			return
		case <-ticker.C:
			// Check connection health using D-Bus
			if c.devicePath != "" {
				deviceObj := c.conn.Object(BLUEZ_BUS_NAME, c.devicePath)
				var connected bool
				var variant dbus.Variant
				if err := deviceObj.Call("org.freedesktop.DBus.Properties.Get", 0, BLUEZ_DEVICE_INTERFACE, "Connected").Store(&variant); err != nil {
					connectionCheckFailures++
					log.Printf("BLE_LOG: Error checking connection (failure %d/3): %v", connectionCheckFailures, err)
					// Only handle connection loss after 3 consecutive failures
					if connectionCheckFailures >= 3 {
						c.handleConnectionLoss()
						return
					}
					continue
				}

				// Extract boolean value from variant
				if connectedVal, ok := variant.Value().(bool); ok {
					connected = connectedVal
				} else {
					log.Printf("BLE_LOG: Warning - could not extract Connected value from variant")
					continue
				}

				if !connected {
					log.Printf("BLE_LOG: Connection lost")
					c.handleConnectionLoss()
					return
				}

				// Reset failure count on successful check
				connectionCheckFailures = 0
			}
		}
	}
}

func (c *BleClient) monitorCharacteristicNotifications() {
	// Add panic recovery for notification monitoring
	defer func() {
		if r := recover(); r != nil {
			log.Printf("BLE_LOG: PANIC in monitorCharacteristicNotifications: %v - restarting monitor", r)
			// Restart the monitor after a delay
			time.Sleep(1 * time.Second)
			go c.monitorCharacteristicNotifications()
		}
	}()

	log.Printf("BLE_LOG: Setting up D-Bus signal monitoring for notifications")

	// Add rate limiting to prevent notification floods
	lastNotificationTime := make(map[string]time.Time)
	notificationMinInterval := 2 * time.Millisecond

	// Subscribe to PropertiesChanged signals for the response characteristic
	rule := fmt.Sprintf("type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',path='%s'", c.responseTxCharPath)

	if err := c.conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, rule).Err; err != nil {
		log.Printf("BLE_LOG: Failed to add match rule for response: %v", err)
		return
	}

	// Add match rule for album art notifications if available
	var albumArtRule string
	if c.albumArtCharPath != "" {
		albumArtRule = fmt.Sprintf("type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',path='%s'", c.albumArtCharPath)
		if err := c.conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, albumArtRule).Err; err != nil {
			log.Printf("BLE_LOG: Failed to add match rule for album art: %v", err)
		}
	}

	// Create a channel to receive signals with smaller buffer to prevent queue buildup
	sigChan := make(chan *dbus.Signal, 10)
	c.conn.Signal(sigChan)

	log.Println("BLE_LOG: Monitoring notifications...")

	for {
		select {
		case <-c.stopChan:
			log.Println("BLE_LOG: Stopping notification monitor")
			c.conn.BusObject().Call("org.freedesktop.DBus.RemoveMatch", 0, rule)
			if albumArtRule != "" {
				c.conn.BusObject().Call("org.freedesktop.DBus.RemoveMatch", 0, albumArtRule)
			}
			return

		case sig := <-sigChan:
			if sig == nil {
				continue
			}

			if sig.Name == "org.freedesktop.DBus.Properties.PropertiesChanged" {
				var charType string
				if sig.Path == c.responseTxCharPath {
					charType = "response"
				} else if c.albumArtCharPath != "" && sig.Path == c.albumArtCharPath {
					charType = "albumart"
				} else {
					continue
				}

				if len(sig.Body) >= 2 {
					if changedProps, ok := sig.Body[1].(map[string]dbus.Variant); ok {
						if valueVariant, exists := changedProps["Value"]; exists {
							if value, ok := valueVariant.Value().([]byte); ok {
								now := time.Now()
								if lastTime, exists := lastNotificationTime[charType]; exists {
									if now.Sub(lastTime) < notificationMinInterval {
										continue
									}
								}
								lastNotificationTime[charType] = now

								valueCopy := make([]byte, len(value))
								copy(valueCopy, value)
								go c.handleNotificationData(valueCopy, charType)
							}
						}
					}
				}
			}
		}
	}
}

func (c *BleClient) handleNotificationData(data []byte, charType string) {
	if len(data) < 3 {
		log.Printf("Received invalid notification data: %v", data)
		return
	}

	msgType := data[0]
	payloadSize := binary.BigEndian.Uint16(data[1:3])
	payload := data[3:]

	if len(payload) != int(payloadSize) {
		log.Printf("Received notification with invalid payload size: %v", data)
		return
	}

	switch msgType {
	case 0x03: // Request
		c.handleRequest(payload)
	default:
		log.Printf("Received unknown notification type: %v", msgType)
	}
}

func (c *BleClient) handleRequest(payload []byte) {
	if len(payload) < 1 {
		log.Printf("Received invalid request payload: %v", payload)
		return
	}

	requestType := payload[0]
	switch requestType {
	case 0x01: // Time Sync
		log.Println("Received time sync request")
		c.handleTimeSyncRequest()
	case 0x02: // Album Art
		log.Println("Received album art request")
		c.handleAlbumArtRequest(payload[1:])
	case 0x03: // Play State
		log.Println("Received play state request")
		c.handlePlayStateRequest()
	case 0x04: // Weather Info
		log.Println("Received weather info request")
		c.handleWeatherInfoRequest()
	default:
		log.Printf("Received unknown request type: %v", requestType)
	}
}

func (c *BleClient) handleTimeSyncRequest() {
	if err := c.sendTimeSyncResponse(); err != nil {
		log.Printf("Failed to send time sync response: %v", err)
	}
}

func (c *BleClient) handleAlbumArtRequest(payload []byte) {
	hash := string(payload)
	log.Printf("Received album art request for hash: %s", hash)

	// TODO: Implement album art cache
	albumArt, err := os.ReadFile("test.webp")
	if err != nil {
		log.Printf("Failed to read album art from cache: %v", err)
		if err := c.sendMessage(0x12, []byte(hash)); err != nil {
			log.Printf("Failed to send album art not found response: %v", err)
		}
		return
	}

	if err := c.sendAlbumArt(hash, albumArt); err != nil {
		log.Printf("Failed to send album art: %v", err)
	}
}

func (c *BleClient) handlePlayStateRequest() {
	if err := c.sendPlayStateUpdate(); err != nil {
		log.Printf("Failed to send play state update: %v", err)
	}
}

func (c *BleClient) handleWeatherInfoRequest() {
	if err := c.sendWeatherInfoUpdate(); err != nil {
		log.Printf("Failed to send weather info update: %v", err)
	}
}
