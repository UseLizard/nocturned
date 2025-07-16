### 1. **Race Condition on Initial Connection:**
*   **`nocturned`:** The `/media/status` endpoint provides the UI with the initial connection state. As suspected, a race condition can occur where the UI requests the status before the BLE connection is fully established.
*   **`NocturneCompanion`:** The Android app sends a "capabilities" message upon connection. This is a good mechanism for the `nocturned` service to know that the client is ready.
*   **Combined Analysis:** The `nocturned` service should wait for the "capabilities" message from the `NocturneCompanion` app before considering the media connection to be fully active. This will resolve the race condition. The `/media/status` endpoint should be updated to reflect this new "fully connected" state.

### 2. **State Update Delays:**
*   **`nocturned`:** The service relies on D-Bus signals for BLE notifications, which can introduce latency.
*   **`NocturneCompanion`:** The Android app sends state updates as notifications on the `STATE_TX_CHAR_UUID`. The `EnhancedBleServerManager.kt` file shows that these notifications are sent immediately when the state changes.
*   **Combined Analysis:** The delay is most likely in the `nocturned` service's handling of the D-Bus signals. The suggestion to implement polling in `nocturned` is still valid. Additionally, I've noticed that the `NocturneCompanion` app has a `DEBUG_LOG_CHAR_UUID` that can be used to stream debug logs. This could be a powerful tool for diagnosing the latency issue. I'll propose a fix that involves enabling this debug log stream and adding more detailed timing information to the logs.

### 3. **ATT Error 0x0e on Rapid Commands:**
*   **`nocturned`:** The service sends commands to the `COMMAND_RX_CHAR_UUID`. The `NOCTURNE_CONNECTION_FIXES_SUMMARY.md` mentions that rapid commands can cause errors.
*   **`NocturneCompanion`:** The Android app receives commands on the `COMMAND_RX_CHAR_UUID` and processes them in the `handleCommandReceived` function. It sends an acknowledgment (ACK) back to the `nocturned` service.
*   **Combined Analysis:** The `nocturned` service is not waiting for the ACK from the `NocturneCompanion` app before sending the next command. This is the root cause of the ATT error. The fix is to implement a command queue in `nocturned` and only send the next command after receiving an ACK for the previous one. The rate-limiting and mutex logic should still be kept as a fallback mechanism.

### 4. **Lingering SPP Logic:**
*   **`nocturned`:** I've found several references to SPP, RFCOMM, and BNEP in the `nocturned` codebase, particularly in the `main.go` file and the `bluetooth` directory.
*   **`NocturneCompanion`:** The Android app's BLE implementation is clean and does not contain any references to SPP.
*   **Combined Analysis:** The lingering SPP logic in `nocturned` is a significant risk. It's likely that some of the connection and profile management code is still trying to use SPP, which could be causing conflicts with the BLE implementation. I'll propose a plan to systematically remove all the SPP-related code from `nocturned`.