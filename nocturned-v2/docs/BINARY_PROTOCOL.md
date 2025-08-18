# Binary Protocol

This document defines the binary protocol for communication between the `nocturned` service and the client.

## Message Format

All messages will have the following format:

```
+----------------+----------------+-----------------+
| Message Type (1) | Payload Size (2) | Payload (n)     |
+----------------+----------------+-----------------+
```

*   **Message Type:** 1 byte, defines the type of the message.
*   **Payload Size:** 2 bytes, defines the size of the payload in bytes.
*   **Payload:** n bytes, the actual payload of the message.

## Message Types

### Control Messages

These messages are sent to the Control Characteristic.

*   **`0x01` - Play State Update:**
    *   **Direction:** Client -> Device
    *   **Payload:**
        ```
        +-----------------+-----------------+-----------------+
        | Track Title (n) | Artist (n)      | Album (n)       |
        +-----------------+-----------------+-----------------+
        | Album Art Hash (n) | Duration (4)    | Position (4)    |
        +-----------------+-----------------+-----------------+
        | Play State (1)  |
        +-----------------+
        ```
*   **`0x02` - Volume Update:**
    *   **Direction:** Client -> Device
    *   **Payload:**
        ```
        +-------------+
        | Volume (1)  |
        +-------------+
        ```
*   **`0x03` - Request:**
    *   **Direction:** Device -> Client
    *   **Payload:**
        ```
        +-----------------+
        | Request Type (1) |
        +-----------------+
        ```

### Album Art Messages

These messages are sent to the Album Art Characteristic.

*   **`0x10` - Album Art Start:**
    *   **Direction:** Client -> Device
    *   **Payload:**
        ```
        +-----------------+-----------------+
        | Total Chunks (2) | Album Art Hash (n) |
        +-----------------+-----------------+
        ```
*   **`0x11` - Album Art Chunk:**
    *   **Direction:** Client -> Device
    *   **Payload:**
        ```
        +--------------+-------------+
        | Chunk Index (2) | Chunk Data (n) |
        +--------------+-------------+
        ```

### Time Sync Messages

These messages are sent to the Time Sync Characteristic.

*   **`0x20` - Time Sync Request:**
    *   **Direction:** Device -> Client
*   **`0x21` - Time Sync Response:**
    *   **Direction:** Client -> Device
    *   **Payload:**
        ```
        +-------------+----------------+
        | Timestamp (8) | Timezone (n)   |
        +-------------+----------------+
        ```
