# BLE API

This document defines the BLE services and characteristics for the `nocturned` v2 service.

## Service

The service UUID will be `13370000-0000-1000-8000-00805F9B34FB`.

## Characteristics

The service will expose the following characteristics:

### Control Characteristic

*   **UUID:** `13370001-0000-1000-8000-00805F9B34FB`
*   **Properties:** Write, Notify
*   **Description:** This characteristic is used for sending commands to the device and receiving notifications from the device.

### Album Art Characteristic

*   **UUID:** `13370002-0000-1000-8000-00805F9B34FB`
*   **Properties:** Write, Notify
*   **Description:** This characteristic is used for transferring album art to the device.

### Time Sync Characteristic

*   **UUID:** `13370003-0000-1000-8000-00805F9B34FB`
*   **Properties:** Write, Notify
*   **Description:** This characteristic is used for time synchronization.
