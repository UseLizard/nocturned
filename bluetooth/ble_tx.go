package bluetooth

import (
	"encoding/binary"
	"fmt"
	"log"
	"time"
)

type Command struct {
	Type    byte
	Payload []byte
}

func (c *BleClient) sendCommand(cmd *Command) {
	c.commandQueue <- cmd
}

func (c *BleClient) processCommandQueue() {
	for {
		select {
		case <-c.stopChan:
			return
		case cmd := <-c.commandQueue:
			if err := c.sendMessage(cmd.Type, cmd.Payload); err != nil {
				log.Printf("Failed to send command: %v", err)
			}
		}
	}
}

func (c *BleClient) sendMessage(msgType byte, payload []byte) error {
	// TODO: Implement rate limiting

	msg := make([]byte, 3+len(payload))
	msg[0] = msgType
	binary.BigEndian.PutUint16(msg[1:3], uint16(len(payload)))
	copy(msg[3:], payload)

	return c.controlChar.Call("org.bluez.GattCharacteristic1.WriteValue", 0, msg, map[string]interface{}{}).Store()
}

func (c *BleClient) sendTimeSyncResponse() error {
	now := time.Now()
	timestamp := now.UnixNano() / int64(time.Millisecond)
	timezone, _ := now.Zone()

	payload := make([]byte, 8+len(timezone))
	binary.BigEndian.PutUint64(payload[:8], uint64(timestamp))
	copy(payload[8:], []byte(timezone))

	return c.sendMessage(0x21, payload)
}

func (c *BleClient) sendPlayStateUpdate() error {
	// TODO: Get actual play state
	trackTitle := "The Trooper"
	artist := "Iron Maiden"
	album := "Piece of Mind"
	albumArtHash := "e0e1e2e3e4e5e6e7e8e9eaebecedeeef"
	duration := 248
	position := 123
	playState := byte(1)

	payload := make([]byte, 0)
	payload = append(payload, []byte(trackTitle)...)
	payload = append(payload, 0)
	payload = append(payload, []byte(artist)...)
	payload = append(payload, 0)
	payload = append(payload, []byte(album)...)
	payload = append(payload, 0)
	payload = append(payload, []byte(albumArtHash)...)
	payload = append(payload, 0)
	binary.BigEndian.PutUint32(payload, uint32(duration))
	binary.BigEndian.PutUint32(payload, uint32(position))
	payload = append(payload, playState)

	return c.sendMessage(0x01, payload)
}

func (c *BleClient) sendWeatherInfoUpdate() error {
	// TODO: Get actual weather info
	weather := "Sunny, 25 C"

	payload := []byte(weather)

	return c.sendMessage(0x30, payload)
}

func (c *BleClient) sendAlbumArt(hash string, data []byte) error {
	chunkSize := 400 // 400 bytes per chunk
	totalChunks := (len(data) + chunkSize - 1) / chunkSize

	// Send start message
	startPayload := make([]byte, 2+len(hash))
	binary.BigEndian.PutUint16(startPayload[:2], uint16(totalChunks))
	copy(startPayload[2:], []byte(hash))
	if err := c.sendMessage(0x10, startPayload); err != nil {
		return fmt.Errorf("failed to send album art start message: %w", err)
	}

	// Send chunks
	for i := 0; i < totalChunks; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(data) {
			end = len(data)
		}

		chunkPayload := make([]byte, 2+len(data[start:end]))
		binary.BigEndian.PutUint16(chunkPayload[:2], uint16(i))
		copy(chunkPayload[2:], data[start:end])

		if err := c.sendMessage(0x11, chunkPayload); err != nil {
			return fmt.Errorf("failed to send album art chunk: %w", err)
		}
	}

	return nil
}
