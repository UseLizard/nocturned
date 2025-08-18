# Nocturned v2

This document outlines the design for the v2 version of the `nocturned` service. The v2 service will be a complete rewrite of the existing service, with a focus on a more robust and efficient BLE-only, binary-only communication protocol.

## High-Level Goals

*   **BLE-only:** The service will only communicate over Bluetooth Low Energy.
*   **Binary-only:** All communication will use a custom binary protocol.
*   **Robust and efficient:** The service will be designed to be reliable and performant, with features like chunking, rate limiting, and command queues.
*   **Feature-rich:** The service will support a variety of features, including:
    *   Connects and disconnects
    *   Play state synchronization
    *   Album art transfer
    *   Request/response mechanism
    *   Time synchronization
    *   Volume control

## Design Documents

*   [BLE API](./BLE_API.md)
*   [Binary Protocol](./BINARY_PROTOCOL.md)
*   [Album Art](./ALBUM_ART.md)

## Follow-up Questions

1.  **Play State:** What specific information should be included in the play state? (e.g., track title, artist, album, duration, current position, playing/paused status)
2.  **Album Art:** Do you have any specific requirements for the image format or size? (e.g., WebP, JPEG, max 512x512)
3.  **Requests:** What kind of requests do you envision the client sending to the device?
4.  **Time Sync:** What is the expected precision? (e.g., seconds, milliseconds)
5.  **Volume:** Should it be a percentage (0-100) or a different scale?
6.  **Other Features:** Are there any other features or requirements that I should consider for this v2 service?
