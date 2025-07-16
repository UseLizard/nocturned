# BLE Comprehensive Fix Plan

## 1. Fix Race Condition - Use Capabilities for Ready State

**Problem**: UI shows disconnected even when BLE is connecting.

**Solution**: Track connection readiness using capabilities message:

```go
// In ble_client.go
type BleClient struct {
    // ... existing fields ...
    fullyConnected bool  // New field
    capsReceived   bool  // New field
}

// In handleNotificationData
case "capabilities":
    // ... existing code ...
    bc.mu.Lock()
    bc.capsReceived = true
    bc.fullyConnected = true
    bc.mu.Unlock()
    
    // Broadcast ready state
    if bc.wsHub != nil {
        bc.wsHub.Broadcast(utils.WebSocketEvent{
            Type: "media/ready",
            Payload: map[string]interface{}{
                "mtu": caps.MTU,
                "version": caps.Version,
            },
        })
    }
```

## 2. Fix State Update Delays - Implement Polling

**Problem**: D-Bus signals have 2-5 second delays.

**Solution**: Active polling of characteristic value:

```go
// Add to ble_client.go
func (bc *BleClient) startStatePolling() {
    ticker := time.NewTicker(500 * time.Millisecond)
    go func() {
        for {
            select {
            case <-bc.stopChan:
                ticker.Stop()
                return
            case <-ticker.C:
                if bc.responseTxCharPath != "" {
                    bc.pollCharacteristicValue()
                }
            }
        }
    }()
}

func (bc *BleClient) pollCharacteristicValue() {
    charObj := bc.conn.Object(BLUEZ_BUS_NAME, bc.responseTxCharPath)
    options := make(map[string]interface{})
    
    var value []byte
    if err := charObj.Call("org.bluez.GattCharacteristic1.ReadValue", 0, options).Store(&value); err == nil && len(value) > 0 {
        // Check if value changed since last poll
        if !bytes.Equal(value, bc.lastPolledValue) {
            bc.lastPolledValue = value
            bc.handleNotificationData(value, "response")
        }
    }
}
```

## 3. Fix ATT Errors - Implement Command Queue with ACK

**Problem**: Rapid commands sent without waiting for ACKs.

**Solution**: Command queue that waits for ACKs:

```go
// Add to ble_client.go
type CommandQueue struct {
    queue       []PendingCommand
    mu          sync.Mutex
    processing  bool
    lastCmdId   string
}

type PendingCommand struct {
    id          string
    command     string
    valueMs     *int
    valuePercent *int
    timestamp   time.Time
}

func (bc *BleClient) SendCommand(command string, valueMs *int, valuePercent *int) error {
    // Generate command ID
    cmdId := fmt.Sprintf("cmd_%d_%d", time.Now().UnixNano(), rand.Int())
    
    // Add to queue
    bc.cmdQueue.mu.Lock()
    bc.cmdQueue.queue = append(bc.cmdQueue.queue, PendingCommand{
        id:          cmdId,
        command:     command,
        valueMs:     valueMs,
        valuePercent: valuePercent,
        timestamp:   time.Now(),
    })
    bc.cmdQueue.mu.Unlock()
    
    // Process queue
    go bc.processCommandQueue()
    return nil
}

// In handleNotificationData, handle ACKs:
case "ack":
    var ack CommandAck
    if err := json.Unmarshal(data, &ack); err == nil {
        if ack.CommandID == bc.cmdQueue.lastCmdId && ack.Status == "success" {
            // Command completed, process next
            bc.cmdQueue.mu.Lock()
            bc.cmdQueue.processing = false
            bc.cmdQueue.mu.Unlock()
            go bc.processCommandQueue()
        }
    }
```

## 4. Remove Lingering SPP Logic

**Files to clean**:
- Remove BNEP monitoring in `bluetooth/manager.go`
- Remove network profile endpoints in `main.go`
- Remove ProfileManager if only used for SPP

**Specific changes**:
```go
// Remove from manager.go:
- monitorNetworkInterfaces()
- All bnep0 references

// Remove from main.go:
- /bluetooth/network endpoints
- /bluetooth/profiles endpoints
- NetworkInfo struct and related code
```

## 5. Enable Debug Log Streaming

**For better diagnostics**:
```go
// In ble_client.go setupCharacteristics()
if bc.debugLogCharPath != "" {
    // Already subscribing to debug logs
    log.Println("BLE_LOG: Debug log streaming enabled")
}

// Parse debug logs in handleNotificationData
case "debugLog":
    // Log with timestamp diff for latency analysis
    var debugLog DebugLogEntry
    if err := json.Unmarshal(data, &debugLog); err == nil {
        latency := time.Now().UnixMilli() - debugLog.Timestamp
        log.Printf("BLE DEBUG [%s] %s: %s (latency: %dms)", 
            debugLog.Level, debugLog.LogType, debugLog.Message, latency)
    }
```

## Implementation Priority

1. **Command Queue with ACK** (fixes crashes)
2. **Polling for state updates** (fixes delays)
3. **Capabilities-based ready state** (fixes initial connection)
4. **Remove SPP logic** (prevents conflicts)
5. **Debug logging** (helps diagnose issues)