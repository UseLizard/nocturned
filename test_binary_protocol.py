#!/usr/bin/env python3
"""
Binary Protocol Album Art Transfer Test
Compares JSON vs Binary protocol performance
"""

import asyncio
import time
import json
import base64
import hashlib
from bleak import BleakClient, BleakScanner
from datetime import datetime
import struct

# Nordic UART Service UUIDs
SERVICE_UUID = "6E400001-B5A3-F393-E0A9-E50E24DCCA9E"
COMMAND_RX_UUID = "6E400002-B5A3-F393-E0A9-E50E24DCCA9E"
STATE_TX_UUID = "6E400003-B5A3-F393-E0A9-E50E24DCCA9E"
ALBUM_ART_TX_UUID = "6E400006-B5A3-F393-E0A9-E50E24DCCA9E"

# Binary protocol constants
PROTOCOL_VERSION = 1
BINARY_HEADER_SIZE = 16
MSG_ALBUM_ART_START = 0x0100
MSG_ALBUM_ART_CHUNK = 0x0101
MSG_ALBUM_ART_END = 0x0102

class BinaryProtocolTester:
    def __init__(self):
        self.client = None
        self.device_address = None
        self.binary_enabled = False
        self.protocol_version = 0
        
        # Transfer tracking
        self.json_stats = {
            "start_time": None,
            "end_time": None,
            "chunks_received": 0,
            "total_chunks": 0,
            "total_bytes": 0,
            "transfer_complete": False
        }
        
        self.binary_stats = {
            "start_time": None,
            "end_time": None,
            "chunks_received": 0,
            "total_chunks": 0,
            "total_bytes": 0,
            "transfer_complete": False
        }
        
    async def find_device(self):
        """Scan for NocturneCompanion device"""
        print("🔍 Scanning for NocturneCompanion device...")
        devices = await BleakScanner.discover()
        
        for device in devices:
            if "NocturneCompanion" in (device.name or ""):
                print(f"✅ Found device: {device.name} ({device.address})")
                return device.address
                
        print("❌ Device not found!")
        return None
        
    def parse_binary_header(self, data):
        """Parse binary protocol header"""
        if len(data) < BINARY_HEADER_SIZE:
            return None
            
        # Unpack header: >HHIII (big-endian: 2 shorts, 3 ints)
        header = struct.unpack('>HHIII', data[:BINARY_HEADER_SIZE])
        return {
            'message_type': header[0],
            'chunk_index': header[1],
            'total_size': header[2],
            'crc32': header[3],
            'reserved': header[4]
        }
        
    def notification_handler(self, sender, data):
        """Handle notifications from device"""
        try:
            # Check if this is binary protocol (album art characteristic)
            if str(sender.uuid).upper() == ALBUM_ART_TX_UUID.upper() and len(data) >= BINARY_HEADER_SIZE:
                # Try to parse as binary
                header = self.parse_binary_header(data)
                if header:
                    self.handle_binary_notification(header, data[BINARY_HEADER_SIZE:])
                    return
            
            # Handle as JSON
            message = json.loads(data.decode('utf-8'))
            msg_type = message.get("type", "")
            
            if msg_type == "capabilities":
                self.binary_enabled = message.get("binary_protocol", False)
                self.protocol_version = message.get("binary_protocol_version", 0)
                print(f"\n📡 Capabilities received:")
                print(f"   Binary protocol: {self.binary_enabled}")
                print(f"   Protocol version: {self.protocol_version}")
                print(f"   Features: {message.get('features', [])}")
                
            elif msg_type == "binary_protocol_enabled":
                self.binary_enabled = True
                self.protocol_version = message.get("version", 1)
                print(f"\n✅ Binary protocol enabled! Version: {self.protocol_version}")
                
            elif msg_type == "album_art_start":
                self.json_stats["start_time"] = time.time()
                self.json_stats["total_chunks"] = message.get("total_chunks", 0)
                self.json_stats["total_bytes"] = message.get("size", 0)
                print(f"\n📦 JSON transfer started: {self.json_stats['total_bytes']} bytes, {self.json_stats['total_chunks']} chunks")
                
            elif msg_type == "album_art_chunk":
                self.json_stats["chunks_received"] += 1
                if self.json_stats["chunks_received"] % 10 == 0:
                    print(f"   JSON progress: {self.json_stats['chunks_received']}/{self.json_stats['total_chunks']}")
                    
            elif msg_type == "album_art_end":
                self.json_stats["end_time"] = time.time()
                self.json_stats["transfer_complete"] = True
                duration = self.json_stats["end_time"] - self.json_stats["start_time"]
                throughput = self.json_stats["total_bytes"] / duration / 1024
                print(f"\n✅ JSON transfer complete!")
                print(f"   Duration: {duration:.2f} seconds")
                print(f"   Throughput: {throughput:.1f} KB/s")
                
        except json.JSONDecodeError:
            pass  # Not JSON data
        except Exception as e:
            print(f"Error handling notification: {e}")
            
    def handle_binary_notification(self, header, payload):
        """Handle binary protocol notifications"""
        msg_type = header['message_type']
        
        if msg_type == MSG_ALBUM_ART_START:
            self.binary_stats["start_time"] = time.time()
            # Parse start payload to get total chunks
            if len(payload) >= 40:
                self.binary_stats["total_chunks"] = struct.unpack('>I', payload[32:36])[0]
                self.binary_stats["total_bytes"] = struct.unpack('>I', payload[36:40])[0]
                print(f"\n📦 Binary transfer started: {self.binary_stats['total_bytes']} bytes, {self.binary_stats['total_chunks']} chunks")
                
        elif msg_type == MSG_ALBUM_ART_CHUNK:
            self.binary_stats["chunks_received"] += 1
            chunk_index = header['chunk_index']
            if self.binary_stats["chunks_received"] % 10 == 0:
                print(f"   Binary progress: {self.binary_stats['chunks_received']}/{self.binary_stats['total_chunks']}")
                
        elif msg_type == MSG_ALBUM_ART_END:
            self.binary_stats["end_time"] = time.time()
            self.binary_stats["transfer_complete"] = True
            duration = self.binary_stats["end_time"] - self.binary_stats["start_time"]
            throughput = self.binary_stats["total_bytes"] / duration / 1024
            print(f"\n✅ Binary transfer complete!")
            print(f"   Duration: {duration:.2f} seconds")
            print(f"   Throughput: {throughput:.1f} KB/s")
            
    async def test_json_transfer(self, client):
        """Test JSON-based album art transfer"""
        print("\n🧪 Testing JSON album art transfer...")
        
        # Reset stats
        self.json_stats = {
            "start_time": None,
            "end_time": None,
            "chunks_received": 0,
            "total_chunks": 0,
            "total_bytes": 0,
            "transfer_complete": False
        }
        
        # Request album art
        query = {
            "command": "album_art_query",
            "hash": "test_json_" + str(int(time.time())),
            "command_id": f"json_test_{int(time.time())}"
        }
        
        await client.write_gatt_char(COMMAND_RX_UUID, json.dumps(query).encode())
        
        # Wait for transfer to complete
        timeout = 10
        start = time.time()
        while not self.json_stats["transfer_complete"] and (time.time() - start) < timeout:
            await asyncio.sleep(0.1)
            
        if not self.json_stats["transfer_complete"]:
            print("❌ JSON transfer timed out!")
            
    async def test_binary_transfer(self, client):
        """Test binary protocol album art transfer"""
        print("\n🧪 Testing binary album art transfer...")
        
        # Reset stats
        self.binary_stats = {
            "start_time": None,
            "end_time": None,
            "chunks_received": 0,
            "total_chunks": 0,
            "total_bytes": 0,
            "transfer_complete": False
        }
        
        # Enable binary protocol first
        if not self.binary_enabled:
            enable_cmd = {
                "command": "enable_binary_protocol",
                "command_id": f"enable_{int(time.time())}"
            }
            await client.write_gatt_char(COMMAND_RX_UUID, json.dumps(enable_cmd).encode())
            await asyncio.sleep(1)  # Wait for protocol to be enabled
        
        # Request album art
        query = {
            "command": "album_art_query",
            "hash": "test_binary_" + str(int(time.time())),
            "command_id": f"binary_test_{int(time.time())}"
        }
        
        await client.write_gatt_char(COMMAND_RX_UUID, json.dumps(query).encode())
        
        # Wait for transfer to complete
        timeout = 10
        start = time.time()
        while not self.binary_stats["transfer_complete"] and (time.time() - start) < timeout:
            await asyncio.sleep(0.1)
            
        if not self.binary_stats["transfer_complete"]:
            print("❌ Binary transfer timed out!")
            
    async def run_test(self):
        """Run the protocol comparison test"""
        self.device_address = await self.find_device()
        if not self.device_address:
            return
            
        print(f"\n🔗 Connecting to {self.device_address}...")
        
        async with BleakClient(self.device_address) as client:
            self.client = client
            
            # Subscribe to notifications
            print("📡 Setting up notifications...")
            await client.start_notify(STATE_TX_UUID, self.notification_handler)
            await client.start_notify(ALBUM_ART_TX_UUID, self.notification_handler)
            
            await asyncio.sleep(1)
            
            # Get MTU and capabilities
            mtu = client.mtu_size
            print(f"📏 Connected with MTU: {mtu}")
            
            # Request capabilities
            cap_cmd = {
                "command": "get_capabilities",
                "command_id": f"cap_{int(time.time())}"
            }
            await client.write_gatt_char(COMMAND_RX_UUID, json.dumps(cap_cmd).encode())
            await asyncio.sleep(1)
            
            # Test JSON transfer
            await self.test_json_transfer(client)
            
            # Test binary transfer if supported
            if self.binary_enabled:
                await self.test_binary_transfer(client)
            else:
                print("\n⚠️  Binary protocol not supported by device")
            
            # Compare results
            print("\n" + "="*60)
            print("📊 PERFORMANCE COMPARISON")
            print("="*60)
            
            if self.json_stats["transfer_complete"]:
                json_duration = self.json_stats["end_time"] - self.json_stats["start_time"]
                json_throughput = self.json_stats["total_bytes"] / json_duration / 1024
                print(f"JSON Protocol:")
                print(f"  Duration: {json_duration:.2f} seconds")
                print(f"  Throughput: {json_throughput:.1f} KB/s")
                print(f"  Chunks: {self.json_stats['total_chunks']}")
                print(f"  Overhead: ~33% (Base64)")
            
            if self.binary_stats["transfer_complete"]:
                binary_duration = self.binary_stats["end_time"] - self.binary_stats["start_time"]
                binary_throughput = self.binary_stats["total_bytes"] / binary_duration / 1024
                print(f"\nBinary Protocol:")
                print(f"  Duration: {binary_duration:.2f} seconds")
                print(f"  Throughput: {binary_throughput:.1f} KB/s")
                print(f"  Chunks: {self.binary_stats['total_chunks']}")
                print(f"  Overhead: <5% (16-byte headers)")
                
                if self.json_stats["transfer_complete"]:
                    improvement = json_duration / binary_duration
                    print(f"\n🚀 Binary protocol is {improvement:.1f}x faster!")
            
            print("\n✨ Test completed!")

async def main():
    tester = BinaryProtocolTester()
    await tester.run_test()

if __name__ == "__main__":
    print("🎨 Binary Protocol Album Art Transfer Test")
    print("="*60)
    print("This test compares JSON vs Binary protocol performance")
    print("="*60)
    print("\nPrerequisites:")
    print("1. NocturneCompanion app with binary protocol support")
    print("2. Music playing with album art")
    print("3. BLE server active")
    print("="*60)
    
    asyncio.run(main())