### **Nocturned Album Art Hashing and Caching Report**

This document outlines the complete process for how album art is identified, requested, received, and cached in the `nocturned` system. It covers two distinct hashing mechanisms: one for generating unique identifiers from metadata and another for ensuring data integrity.

---

#### **1. Key Files**

The logic for handling album art is primarily split between two files:

*   `utils/album_art.go`: Contains the core functions for hash generation, path creation, and file I/O.
*   `bluetooth/ble_client.go`: Implements the logic for checking the cache, requesting art from a connected device, and processing the received image data.

---

#### **2. Mechanism 1: Metadata Hashing for Identification (MD5)**

This mechanism creates a unique, consistent identifier for a piece of album art based on its artist and album metadata. This identifier is used as the filename for the cached image.

##### **A. Hash Generation**

The `GenerateAlbumArtHash` function is responsible for creating the identifier.

*   **File:** `utils/album_art.go`
*   **Function:** `GenerateAlbumArtHash`

```go
// GenerateAlbumArtHash generates a hash from artist and album names
func GenerateAlbumArtHash(artist, album string) string {
	// Normalize strings: lowercase, trim spaces
	artist = strings.TrimSpace(strings.ToLower(artist))
	album = strings.TrimSpace(strings.ToLower(album))
	
	// Combine artist and album
	combined := artist + "-" + album
	
	// Generate MD5 hash
	hash := md5.Sum([]byte(combined))
	return hex.EncodeToString(hash[:])
}
```

*   **Process:**
    1.  **Normalization:** It takes the `artist` and `album` strings, converts them to lowercase, and removes any leading/trailing whitespace. This ensures that minor string variations don't result in different hashes.
    2.  **Combination:** It concatenates the normalized strings with a hyphen.
    3.  **Hashing:** It computes the **MD5** hash of the combined string.
    4.  **Encoding:** The resulting binary hash is encoded into a hexadecimal string, which serves as the unique ID.

##### **B. Cache Path and Checking**

This unique ID is used to determine the file path for the cached image and to check if it already exists.

*   **File:** `utils/album_art.go`
*   **Functions:** `GetAlbumArtPath`, `CheckAlbumArtExists`

```go
// GetAlbumArtPath returns the path for cached album art
func GetAlbumArtPath(artist, album string) string {
	hash := GenerateAlbumArtHash(artist, album)
	return filepath.Join("/data/etc/nocturne/albumart", hash + ".webp")
}

// CheckAlbumArtExists checks if album art is already cached
func CheckAlbumArtExists(artist, album string) bool {
	path := GetAlbumArtPath(artist, album)
	_, err := os.Stat(path)
	return err == nil
}
```

*   **Process:**
    1.  `GetAlbumArtPath` calls `GenerateAlbumArtHash` to get the MD5 hash and constructs the full, absolute path where the `.webp` image should be stored.
    2.  `CheckAlbumArtExists` uses this path with `os.Stat` to efficiently check if the file is already present in the cache.

##### **C. Requesting Album Art**

The `ble_client.go` file uses this mechanism to avoid requesting art that is already cached.

*   **File:** `bluetooth/ble_client.go`
*   **Function:** `checkAndRequestAlbumArt`

```go
func (bc *BleClient) checkAndRequestAlbumArt(artist, album string) {
	// Check if album art is already cached
	if utils.CheckAlbumArtExists(artist, album) {
		// TEMP log.Printf("BLE_LOG: Album art already cached for %s - %s", artist, album)
		// ... (broadcasts that art is cached)
		return
	}
	
	// Generate hash for this artist/album combination
	hash := utils.GenerateAlbumArtHash(artist, album)
	// TEMP log.Printf("BLE_LOG: Album art not cached for %s - %s (hash: %s), requesting from companion", artist, album, hash)
	
	// ... (sends request to companion app with the MD5 hash)
}
```

---

#### **3. Mechanism 2: Data Integrity Checksum (SHA-256)**

This mechanism is used to verify that the received album art image data has not been corrupted during transmission.

##### **A. Checksum Calculation**

*   **File:** `utils/album_art.go`
*   **Function:** `CalculateSHA256`

```go
// CalculateSHA256 calculates the SHA-256 checksum of the given data
func CalculateSHA256(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
```

*   **Process:** This function takes the raw byte slice of the image data, computes its **SHA-256** hash, and returns it as a hexadecimal string.

##### **B. Verification and Saving**

The `ble_client.go` file uses this checksum to validate the received data before saving it.

*   **File:** `bluetooth/ble_client.go`
*   **Function:** `processAlbumArt`

```go
func (bc *BleClient) processAlbumArt() {
	// ... (mutex lock)
	
	// Verify checksum (using SHA-256 to match Android app)
	checksum := utils.CalculateSHA256(bc.albumArtBuffer)
	if checksum != bc.albumArtChecksum {
		// TEMP log.Printf("BLE_LOG: Album art checksum mismatch - expected: %s, got: %s", 
			bc.albumArtChecksum, checksum)
		// ... (aborts processing)
		return
	}
	
	// ... (extracts artist/album from current state)
	
	// Save album art with the MD5 hash-based filename for caching
	if artist != "" && album != "" {
		cacheFilename := utils.GetAlbumArtPath(artist, album) // Uses MD5 hash
		if err := utils.SaveAlbumArt(bc.albumArtBuffer, cacheFilename); err != nil {
			// ... (logs error)
		}
		// ... (saves metadata)
	}
	// ...
}
```

*   **File:** `utils/album_art.go`
*   **Function:** `SaveAlbumArt`

```go
// SaveAlbumArt saves album art data to a file
func SaveAlbumArt(data []byte, filename string) error {
	// Ensure directory exists
	dir := filepath.Dir(filename)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}
	
	// Write file
	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}
	
	return nil
}
```

---

#### **4. Summary**

The `nocturned` service employs a two-hash system for album art management:

1.  **MD5 of Metadata (Artist + Album):** Used as a **unique identifier** to name the cached file and to check for its existence. This is the primary mechanism for the caching logic.
2.  **SHA-256 of Image Data:** Used as a **data integrity checksum** to verify that the received image file has not been corrupted during transfer.

This dual approach ensures both efficient caching and reliable data transmission.