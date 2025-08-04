# nocturned - BLE Media Control Service

## Architecture
Go service on Void Linux ARM device. Communicates with Android NocturneCompanion app via BLE Nordic UART Service.

## Key Files
- `bluetooth/ble_client.go`: BLE client implementation
- `bluetooth/manager.go`: Bluetooth device management
- `main.go`: HTTP API, WebSocket hub
- `utils/websocket.go`: WebSocket client management

## BLE Protocol
### Characteristics
- CommandRx (6e400002): nocturned→Android commands
- ResponseTx (6e400003): Android→nocturned notifications
- AlbumArtTx (6e400006): Album art chunks

### Command Format
```json
{"command":"play","command_id":"cmd_123","value_ms":1000,"value_percent":50}
```

### ACK Format
```json
{"type":"ack","command_id":"cmd_123","status":"received|success"}
```

### State Update
```json
{"type":"stateUpdate","artist":"X","album":"Y","track":"Z","duration_ms":180000,"position_ms":45000,"is_playing":true,"volume_percent":75}
```

## Common Issues
1. ACK timeout: Android must echo command_id in ACK
2. MTU overflow: Keep notifications <509 bytes
3. Connection health checked every 60s
4. Notifications rate limited to 15ms

## Build/Run
```bash
go build
./nocturned -addr :8080
```

## API Endpoints
- GET /api/bluetooth/devices
- POST /api/bluetooth/connect
- POST /api/bluetooth/disconnect
- POST /api/media/control
- GET /api/media/state
- WS /ws