#!/usr/bin/env python3
"""
Album Art Transfer Performance Test Script
Tests the BLE album art transfer performance between Android and nocturned
"""

import asyncio
import time
import json
import base64
import hashlib
from bleak import BleakClient, BleakScanner
from datetime import datetime

# Nordic UART Service UUIDs
SERVICE_UUID = "6E400001-B5A3-F393-E0A9-E50E24DCCA9E"
COMMAND_RX_UUID = "6E400002-B5A3-F393-E0A9-E50E24DCCA9E"  # Write
STATE_TX_UUID = "6E400003-B5A3-F393-E0A9-E50E24DCCA9E"    # Notify
ALBUM_ART_TX_UUID = "6E400006-B5A3-F393-E0A9-E50E24DCCA9E" # Notify

class AlbumArtTester:
    def __init__(self):
        self.client = None
        self.device_address = None
        self.transfer_start_time = None
        self.transfer_stats = {
            "chunks_received": 0,
            "total_chunks": 0,
            "start_received": False,
            "end_received": False,
            "total_bytes": 0,
            "checksum": None
        }
        
    async def find_device(self):
        """Scan for NocturneCompanion device"""
        print("Scanning for NocturneCompanion device...")
        devices = await BleakScanner.discover()
        
        for device in devices:
            if "NocturneCompanion" in (device.name or ""):
                print(f"Found device: {device.name} ({device.address})")
                return device.address
                
        print("Device not found!")
        return None
        
    def notification_handler(self, sender, data):
        """Handle notifications from the device"""
        try:
            # Try to parse as JSON
            message = json.loads(data.decode('utf-8'))
            msg_type = message.get("type", "")
            
            if msg_type == "album_art_start":
                self.transfer_start_time = time.time()
                self.transfer_stats["start_received"] = True
                self.transfer_stats["total_chunks"] = message.get("total_chunks", 0)
                self.transfer_stats["total_bytes"] = message.get("size", 0)
                self.transfer_stats["checksum"] = message.get("checksum", "")
                print(f"\n📦 Album art transfer started:")
                print(f"   Size: {self.transfer_stats['total_bytes']} bytes")
                print(f"   Chunks: {self.transfer_stats['total_chunks']}")
                print(f"   Checksum: {self.transfer_stats['checksum'][:16]}...")
                
            elif msg_type == "album_art_chunk":
                self.transfer_stats["chunks_received"] += 1
                chunk_index = message.get("chunk_index", -1)
                
                # Progress indicator every 10 chunks
                if chunk_index % 10 == 0:
                    progress = (self.transfer_stats["chunks_received"] / self.transfer_stats["total_chunks"]) * 100
                    elapsed = time.time() - self.transfer_start_time
                    rate = self.transfer_stats["chunks_received"] / elapsed if elapsed > 0 else 0
                    print(f"\r   Progress: {progress:.1f}% ({self.transfer_stats['chunks_received']}/{self.transfer_stats['total_chunks']}) - {rate:.1f} chunks/sec", end="")
                    
            elif msg_type == "album_art_end":
                self.transfer_stats["end_received"] = True
                success = message.get("success", False)
                
                if success and self.transfer_start_time:
                    elapsed = time.time() - self.transfer_start_time
                    print(f"\n\n✅ Transfer completed successfully!")
                    print(f"   Total time: {elapsed:.2f} seconds")
                    print(f"   Average speed: {self.transfer_stats['total_bytes'] / elapsed / 1024:.1f} KB/s")
                    print(f"   Chunks/sec: {self.transfer_stats['chunks_received'] / elapsed:.1f}")
                    
                    # Calculate improvement vs old implementation
                    old_chunk_size = 400
                    old_chunks = (self.transfer_stats['total_bytes'] * 1.33) / old_chunk_size  # Base64 overhead
                    old_time = old_chunks * 0.030  # 30ms per chunk
                    improvement = old_time / elapsed
                    print(f"\n🚀 Performance improvement: {improvement:.1f}x faster than baseline")
                else:
                    print(f"\n❌ Transfer failed!")
                    
            elif msg_type == "stateUpdate":
                # State update received
                artist = message.get("artist", "Unknown")
                album = message.get("album", "Unknown")
                title = message.get("track", "Unknown")
                print(f"\n🎵 Current track: {artist} - {title} ({album})")
                
        except json.JSONDecodeError:
            # Not JSON, might be raw data
            pass
        except Exception as e:
            print(f"\nError handling notification: {e}")
            
    async def test_album_art_query(self, client):
        """Send an album art query to trigger transfer"""
        # Generate a test MD5 hash (you'd normally get this from actual media)
        test_artist = "test artist"
        test_album = "test album"
        combined = f"{test_artist.lower().strip()}-{test_album.lower().strip()}"
        md5_hash = hashlib.md5(combined.encode()).hexdigest()
        
        query = {
            "command": "album_art_query",
            "hash": md5_hash,
            "command_id": f"test_{int(time.time())}"
        }
        
        print(f"\n📤 Sending album art query with hash: {md5_hash}")
        await client.write_gatt_char(COMMAND_RX_UUID, json.dumps(query).encode())
        
    async def run_test(self):
        """Run the album art transfer performance test"""
        # Find device
        self.device_address = await self.find_device()
        if not self.device_address:
            return
            
        print(f"\n🔗 Connecting to {self.device_address}...")
        
        async with BleakClient(self.device_address) as client:
            self.client = client
            
            # Subscribe to notifications
            print("📡 Subscribing to notifications...")
            await client.start_notify(STATE_TX_UUID, self.notification_handler)
            await client.start_notify(ALBUM_ART_TX_UUID, self.notification_handler)
            
            # Wait a bit for connection to stabilize
            await asyncio.sleep(1)
            
            # Get MTU info
            mtu = client.mtu_size
            print(f"📏 Connected with MTU: {mtu}")
            
            # Request state update first
            print("\n📊 Requesting current state...")
            state_cmd = {
                "command": "get_state",
                "command_id": f"state_{int(time.time())}"
            }
            await client.write_gatt_char(COMMAND_RX_UUID, json.dumps(state_cmd).encode())
            await asyncio.sleep(1)
            
            # Test album art transfer
            print("\n🎨 Testing album art transfer...")
            await self.test_album_art_query(client)
            
            # Wait for transfer to complete (timeout after 10 seconds)
            timeout = 10
            start = time.time()
            while not self.transfer_stats["end_received"] and (time.time() - start) < timeout:
                await asyncio.sleep(0.1)
                
            if not self.transfer_stats["end_received"]:
                print("\n⏱️  Transfer timed out!")
                
            # Summary
            print("\n" + "="*50)
            print("📊 TRANSFER SUMMARY")
            print("="*50)
            print(f"Chunks received: {self.transfer_stats['chunks_received']}/{self.transfer_stats['total_chunks']}")
            if self.transfer_start_time and self.transfer_stats["end_received"]:
                total_time = time.time() - self.transfer_start_time
                print(f"Transfer time: {total_time:.2f} seconds")
                print(f"Effective throughput: {self.transfer_stats['total_bytes'] / total_time / 1024:.1f} KB/s")
            
            print("\n✨ Test completed!")

async def main():
    tester = AlbumArtTester()
    await tester.run_test()

if __name__ == "__main__":
    print("🎨 Album Art Transfer Performance Tester")
    print("="*50)
    print("Make sure:")
    print("1. NocturneCompanion app is running with BLE server active")
    print("2. You have music playing with album art")
    print("3. Bluetooth is enabled on this device")
    print("="*50)
    
    asyncio.run(main())