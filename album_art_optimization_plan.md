# Bluetooth Album Art Transfer Optimization Plan

## Overview
This plan outlines the implementation steps to optimize BLE album art transfer between the Android app (NocturneCompanion) and the Car Thing (nocturned), targeting an 8x performance improvement.

## Current Performance Baseline
- **Image Size**: 25KB WebP (300x300px)
- **Transfer Size**: ~33KB (Base64 encoded)
- **Chunks**: ~85 chunks @ 400 bytes each
- **Transfer Time**: ~2.5 seconds
- **Overhead**: ~40% (Base64 + JSON)

## Target Performance
- **Transfer Size**: 25KB (raw binary)
- **Chunks**: ~30 chunks @ 500 bytes each
- **Transfer Time**: ~0.3 seconds
- **Overhead**: <5% (binary headers)

## Implementation Tasks

### Phase 1: Remove Artificial Delays (Quick Win)
**Estimated Impact**: 2x speedup
**Effort**: Low (1-2 hours)

- [ ] Modify `MessageQueue.kt` MIN_BULK_INTERVAL_MS from 30ms to 5ms
- [ ] Increase MAX_BULK_MESSAGES_PER_SECOND from 30 to 200
- [ ] Remove unnecessary delays in `sendAlbumArtToDevice()`
- [ ] Test stability with reduced delays

### Phase 2: Optimize Chunk Size
**Estimated Impact**: 1.5x speedup
**Effort**: Low (2-3 hours)

- [ ] Remove hardcoded 400-byte chunk limit in `EnhancedBleServerManager.kt`
- [ ] Calculate dynamic chunk size based on actual MTU: `effectiveMtu - jsonOverhead`
- [ ] Update jsonOverhead calculation to be more accurate (measure actual JSON size)
- [ ] Add MTU negotiation logging to verify we're getting 517 bytes
- [ ] Test with various Android devices to ensure compatibility

### Phase 3: Implement Binary Protocol
**Estimated Impact**: 2x speedup + 33% less data
**Effort**: High (8-12 hours)

#### Android Side (NocturneCompanion):
- [ ] Create `BinaryProtocol.kt` with binary message structures
- [ ] Define binary header format (16 bytes):
  ```
  [0-1]   Message Type (uint16)
  [2-3]   Chunk Index (uint16) 
  [4-7]   Total Size (uint32)
  [8-11]  Checksum CRC32 (uint32)
  [12-15] Reserved (uint32)
  [16+]   Raw Data
  ```
- [ ] Implement `BinaryAlbumArtEncoder` to replace JSON/Base64
- [ ] Add binary message support to `MessageQueue.kt`
- [ ] Update `sendAlbumArtToDevice()` to use binary protocol
- [ ] Add protocol version negotiation in capabilities exchange

#### Go Side (nocturned):
- [ ] Create `binary_protocol.go` with matching structures
- [ ] Implement binary message parser in `handleNotification()`
- [ ] Update album art chunk assembly for binary data
- [ ] Add CRC32 validation for chunks
- [ ] Maintain backward compatibility with JSON protocol

### Phase 4: Sliding Window Protocol
**Estimated Impact**: 1.5x speedup
**Effort**: Medium (4-6 hours)

- [ ] Implement chunk acknowledgment system:
  - [ ] Send chunks in batches of 10
  - [ ] Track sent/acknowledged chunks with bitmap
  - [ ] Implement selective retransmission
- [ ] Add flow control to prevent overwhelming receiver
- [ ] Implement congestion detection and backoff
- [ ] Add metrics for packet loss and retransmission rate

### Phase 5: Compression Layer
**Estimated Impact**: 1.2x speedup (10-20% size reduction)
**Effort**: Medium (3-4 hours)

- [ ] Add zlib compression after WebP encoding
- [ ] Implement compression negotiation in capabilities
- [ ] Add decompression on nocturned side
- [ ] Benchmark compression ratios and CPU impact
- [ ] Make compression optional based on image size threshold

### Phase 6: Advanced Caching
**Estimated Impact**: Instant for cached images
**Effort**: Medium (4-5 hours)

- [ ] Implement bloom filter for quick cache checks
- [ ] Add pre-fetch on connection:
  - [ ] Query nocturned for missing album art hashes
  - [ ] Background transfer of likely-needed art
- [ ] Implement differential updates:
  - [ ] Detect similar album art (same artist)
  - [ ] Send only differences
- [ ] Add cache size management and eviction policy

## Testing Plan

### Unit Tests
- [ ] Binary protocol encoding/decoding
- [ ] Chunk assembly with missing chunks
- [ ] Compression/decompression
- [ ] Flow control and congestion handling

### Integration Tests
- [ ] End-to-end transfer with various image sizes
- [ ] Network interruption recovery
- [ ] Multiple concurrent transfers
- [ ] Backward compatibility with JSON protocol

### Performance Tests
- [ ] Benchmark transfer times for various image sizes
- [ ] Measure CPU and memory usage
- [ ] Test with poor network conditions
- [ ] Verify no impact on audio playback

## Rollout Strategy

1. **Phase 1-2**: Deploy immediately as low-risk optimizations
2. **Phase 3**: Beta test with protocol version flag
3. **Phase 4-5**: A/B test to measure real-world impact
4. **Phase 6**: Deploy after monitoring cache hit rates

## Success Metrics

- Transfer time for 25KB image < 0.5 seconds
- Zero impact on audio control responsiveness  
- Packet loss rate < 1%
- Cache hit rate > 80% for repeated albums
- No increase in crash rate

## Risk Mitigation

- Maintain JSON protocol fallback for compatibility
- Add feature flags for each optimization
- Implement comprehensive error handling
- Monitor performance metrics in production
- Have rollback plan for each phase

## Timeline

- **Week 1**: Phase 1-2 (Quick wins)
- **Week 2-3**: Phase 3 (Binary protocol)
- **Week 4**: Phase 4-5 (Sliding window + compression)
- **Week 5**: Phase 6 (Advanced caching)
- **Week 6**: Testing and rollout

Total estimated effort: 40-50 hours