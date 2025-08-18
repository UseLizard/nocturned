package listeners

import (
	"log"
	"time"

	ping "github.com/prometheus-community/pro-bing"
	"github.com/usenocturne/nocturned/utils"
)

var currentNetworkStatus = "offline"

func NetworkChecker(hub *utils.WebSocketHub) {
	const (
		host          = "1.1.1.1"
		interval      = 1 // seconds
		failThreshold = 3
	)

	failCount := 0

	isOnline := false
	pinger, err := ping.NewPinger(host)
	if err == nil {
		pinger.Count = 1
		pinger.Timeout = 1 * time.Second
		pinger.Interval = 1 * time.Second
		pinger.SetPrivileged(true)
		err = pinger.Run()
		if err == nil && pinger.Statistics().PacketsRecv > 0 {
			currentNetworkStatus = "online"
			hub.Broadcast(utils.WebSocketEvent{
				Type:    "network_status",
				Payload: map[string]string{"status": "online"},
			})
			isOnline = true
		} else {
			currentNetworkStatus = "offline"
			hub.Broadcast(utils.WebSocketEvent{
				Type:    "network_status",
				Payload: map[string]string{"status": "offline"},
			})
		}
	} else {
		hub.Broadcast(utils.WebSocketEvent{
			Type:    "network_status",
			Payload: map[string]string{"status": "offline"},
		})
	}

	for {
		pinger, err := ping.NewPinger(host)
		if err != nil {
			log.Printf("Failed to create pinger: %v", err)
			failCount++
		} else {
			pinger.Count = 1
			pinger.Timeout = 1 * time.Second
			pinger.Interval = 1 * time.Second
			pinger.SetPrivileged(true)
			err = pinger.Run()
			if err != nil || pinger.Statistics().PacketsRecv == 0 {
				failCount++
			} else {
				failCount = 0
				if !isOnline {
					currentNetworkStatus = "online"
					hub.Broadcast(utils.WebSocketEvent{
						Type:    "network_status",
						Payload: map[string]string{"status": "online"},
					})
					isOnline = true
				}
			}
		}

		if failCount >= failThreshold && isOnline {
			currentNetworkStatus = "offline"
			hub.Broadcast(utils.WebSocketEvent{
				Type:    "network_status",
				Payload: map[string]string{"status": "offline"},
			})
			isOnline = false
		}

		time.Sleep(interval * time.Second)
	}
}

func GetCurrentNetworkStatus() string {
	return currentNetworkStatus
}
