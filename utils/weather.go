package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// WeatherUpdate represents a weather update message from Android
type WeatherUpdate struct {
	Type      string          `json:"type"`
	Mode      string          `json:"mode"` // "hourly" or "weekly"
	Location  WeatherLocation `json:"location"`
	Timestamp int64           `json:"timestamp_ms"`
	Hours     []HourlyData    `json:"hours,omitempty"`
	Days      []DailyData     `json:"days,omitempty"`
}

// WeatherLocation represents the location for weather data
type WeatherLocation struct {
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// HourlyData represents hourly weather information
type HourlyData struct {
	Time          string `json:"time"`          // ISO format: "2025-01-14T10:00"
	TempF         int    `json:"temp_f"`        // Temperature in Fahrenheit
	TempC         int    `json:"temp_c"`        // Temperature in Celsius
	Condition     string `json:"condition"`     // Human readable condition
	WeatherCode   int    `json:"weather_code"`  // Open-Meteo weather code
	Precipitation int    `json:"precipitation"` // Precipitation probability %
	Humidity      int    `json:"humidity"`      // Relative humidity %
	WindSpeed     int    `json:"wind_speed"`    // Wind speed in mph
}

// DailyData represents daily weather information
type DailyData struct {
	Date          string `json:"date"`          // ISO format: "2025-01-14"
	DayName       string `json:"day_name"`      // "Monday", "Tuesday", etc.
	HighF         int    `json:"high_f"`        // High temperature in Fahrenheit
	LowF          int    `json:"low_f"`         // Low temperature in Fahrenheit
	HighC         int    `json:"high_c"`        // High temperature in Celsius
	LowC          int    `json:"low_c"`         // Low temperature in Celsius
	Condition     string `json:"condition"`     // Human readable condition
	WeatherCode   int    `json:"weather_code"`  // Open-Meteo weather code
	Precipitation int    `json:"precipitation"` // Precipitation probability %
	Humidity      int    `json:"humidity"`      // Average humidity %
}

// WeatherState holds the current weather state
type WeatherState struct {
	LastUpdate   time.Time       `json:"last_update"`
	HourlyData   *WeatherUpdate  `json:"hourly_data,omitempty"`
	WeeklyData   *WeatherUpdate  `json:"weekly_data,omitempty"`
}

// ToJSON converts WeatherUpdate to JSON string
func (w *WeatherUpdate) ToJSON() (string, error) {
	data, err := json.Marshal(w)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// FromJSON creates WeatherUpdate from JSON string
func WeatherUpdateFromJSON(jsonStr string) (*WeatherUpdate, error) {
	var update WeatherUpdate
	err := json.Unmarshal([]byte(jsonStr), &update)
	if err != nil {
		return nil, err
	}
	return &update, nil
}

// GetConditionFromCode returns human-readable condition from weather code
func GetConditionFromCode(code int) string {
	switch code {
	case 0:
		return "Clear"
	case 1:
		return "Partly Cloudy"
	case 2, 3:
		return "Cloudy"
	case 45, 48:
		return "Foggy"
	case 51, 53, 55:
		return "Drizzle"
	case 56, 57:
		return "Freezing Drizzle"
	case 61, 63, 65:
		return "Rain"
	case 66, 67:
		return "Freezing Rain"
	case 71, 73, 75, 77:
		return "Snow"
	case 80, 81, 82:
		return "Rain Showers"
	case 85, 86:
		return "Snow Showers"
	case 95:
		return "Thunderstorm"
	case 96, 99:
		return "Heavy Thunderstorm"
	default:
		return "Unknown"
	}
}

// IsRain returns true if the weather code indicates rain/precipitation
func IsRain(code int) bool {
	switch code {
	case 51, 53, 55, 56, 57: // Drizzle
		return true
	case 61, 63, 65, 66, 67: // Rain
		return true
	case 80, 81, 82: // Rain showers
		return true
	case 95, 96, 99: // Thunderstorms
		return true
	default:
		return false
	}
}

// IsSnow returns true if the weather code indicates snow
func IsSnow(code int) bool {
	switch code {
	case 71, 73, 75, 77: // Snow
		return true
	case 85, 86: // Snow showers
		return true
	default:
		return false
	}
}

// IsClear returns true if the weather code indicates clear/sunny conditions
func IsClear(code int) bool {
	return code == 0
}

// IsCloudy returns true if the weather code indicates cloudy conditions
func IsCloudy(code int) bool {
	switch code {
	case 1, 2, 3: // Partly to fully cloudy
		return true
	default:
		return false
	}
}

// SaveWeatherData saves weather data to /var/nocturne/weather/ folder
// Files: hourly_weather.json, weekly_weather.json
// Format: timestamp on first line, then JSON data
func SaveWeatherData(hourlyData, weeklyData *WeatherUpdate) error {
	weatherDir := "/var/nocturne/weather"
	
	// Create weather directory
	if err := os.MkdirAll(weatherDir, 0755); err != nil {
		return fmt.Errorf("failed to create weather directory %s: %v", weatherDir, err)
	}
	
	currentTime := time.Now()
	timestamp := currentTime.Unix()
	
	// Save hourly data if provided
	if hourlyData != nil {
		hourlyPath := filepath.Join(weatherDir, "hourly_weather.json")
		if err := saveWeatherFileWithTimestamp(hourlyData, hourlyPath, timestamp); err != nil {
			return fmt.Errorf("failed to save hourly data: %v", err)
		}
	}
	
	// Save weekly data if provided  
	if weeklyData != nil {
		weeklyPath := filepath.Join(weatherDir, "weekly_weather.json")
		if err := saveWeatherFileWithTimestamp(weeklyData, weeklyPath, timestamp); err != nil {
			return fmt.Errorf("failed to save weekly data: %v", err)
		}
	}
	
	return nil
}

// saveWeatherFileWithTimestamp saves weather data with timestamp embedded in JSON
func saveWeatherFileWithTimestamp(data *WeatherUpdate, filepath string, timestamp int64) error {
	// Ensure the timestamp is set in the data structure
	if data.Timestamp == 0 {
		data.Timestamp = timestamp * 1000 // Convert to milliseconds
	}
	
	// Convert to JSON with proper formatting
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	
	return os.WriteFile(filepath, jsonData, 0644)
}

// LoadWeatherFile loads weather data from JSON file with embedded timestamp
func LoadWeatherFile(filepath string) (*WeatherUpdate, int64, error) {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, 0, err
	}
	
	var weatherUpdate WeatherUpdate
	if err := json.Unmarshal(data, &weatherUpdate); err != nil {
		return nil, 0, fmt.Errorf("failed to parse weather JSON: %v", err)
	}
	
	// Extract timestamp from the JSON structure (convert from milliseconds to seconds)
	timestamp := weatherUpdate.Timestamp / 1000
	
	return &weatherUpdate, timestamp, nil
}

// IsWeatherFileRecent checks if weather file is recent (within specified duration)
func IsWeatherFileRecent(filepath string, maxAge time.Duration) bool {
	_, timestamp, err := LoadWeatherFile(filepath)
	if err != nil {
		return false
	}
	
	age := time.Now().Unix() - timestamp
	return time.Duration(age)*time.Second < maxAge
}