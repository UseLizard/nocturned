# Album Art Transfer

This document defines the album art transfer mechanism for the `nocturned` v2 service.

## Image Format

*   **Format:** WebP
*   **Size:** 300x300

## Transfer Mechanism

The album art is transferred using a chunking mechanism over the Album Art Characteristic.

1.  The client sends an **Album Art Start** message to the device. This message contains the total number of chunks and the hash of the album art.
2.  The client sends a series of **Album Art Chunk** messages to the device. Each message contains the chunk index and the chunk data.
3.  The device receives the chunks and reassembles the album art.
4.  The device verifies the checksum of the reassembled album art against the hash received in the **Album Art Start** message.
5.  The device sends a notification to the client to indicate whether the transfer was successful or not.
