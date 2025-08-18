package bluetooth

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"time"
)

// GradientColor represents a color with RGB values
type GradientColor struct {
	R, G, B uint8
}

// OrganicLayer represents a single layer in the organic gradient
type OrganicLayer struct {
	Colors []GradientColor
	Center image.Point
	Radius float64
	Alpha  float64
	Type   string // "radial", "linear", "sweep"
}

// ParseGradientColorsPayload parses the gradient colors from binary payload
func ParseGradientColorsPayload(data []byte) ([]GradientColor, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty gradient colors payload")
	}

	colorCount := int(data[0])
	if len(data) < 1+colorCount*3 {
		return nil, fmt.Errorf("invalid gradient colors payload size")
	}

	colors := make([]GradientColor, colorCount)
	for i := 0; i < colorCount; i++ {
		offset := 1 + i*3
		colors[i] = GradientColor{
			R: data[offset],
			G: data[offset+1],
			B: data[offset+2],
		}
	}

	return colors, nil
}

// GenerateLayeredGradientImage creates an organic layered gradient image similar to NocturneCompanion
func GenerateLayeredGradientImage(colors []GradientColor, width, height int) (*image.RGBA, error) {
	if len(colors) == 0 {
		return nil, fmt.Errorf("no colors provided")
	}

	// Create base image
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Generate seed from colors for consistent randomization
	seed := int64(0)
	for _, c := range colors {
		seed += int64(c.R) + int64(c.G) + int64(c.B)
	}
	rng := rand.New(rand.NewSource(seed))

	// Fill with subtle vibrant background color (matching companion app)
	vibrantColor := getMostVibrantColor(colors)
	backgroundColor := color.RGBA{
		R: vibrantColor.R,
		G: vibrantColor.G,
		B: vibrantColor.B,
		A: 31, // ~0.12f * 255 = 31, very subtle background
	}
	draw.Draw(img, img.Bounds(), &image.Uniform{backgroundColor}, image.Point{}, draw.Src)

	// Add linear gradient background layer (like companion app)
	applyLinearGradientBackground(img, colors, width, height)

	// Add radial gradient background layer (like companion app)
	applyRadialGradientBackground(img, colors, width, height, rng)

	// Skip diagonal background layer to avoid line artifacts
	// applyDiagonalGradientBackground(img, colors, width, height)

	// Create enhanced organic layers (matching companion app logic)
	layers := createEnhancedOrganicLayers(colors, width, height, rng)

	// Apply each organic layer to the image
	for _, layer := range layers {
		applyOrganicLayer(img, layer, width, height)
	}

	// Apply stronger blur effect for softer, more organic appearance
	blurredImg := applyGaussianBlur(img, 2.5) // Increased radius for softer blur

	return blurredImg, nil
}

// getMostVibrantColor finds the color with highest saturation
func getMostVibrantColor(colors []GradientColor) GradientColor {
	if len(colors) == 0 {
		return GradientColor{128, 128, 128} // gray fallback
	}

	mostVibrant := colors[0]
	maxVibrance := getVibrance(colors[0])

	for _, c := range colors[1:] {
		vibrance := getVibrance(c)
		if vibrance > maxVibrance {
			maxVibrance = vibrance
			mostVibrant = c
		}
	}

	return mostVibrant
}

// getVibrance calculates the vibrance (saturation) of a color
func getVibrance(c GradientColor) float64 {
	r := float64(c.R) / 255.0
	g := float64(c.G) / 255.0
	b := float64(c.B) / 255.0

	maxVal := math.Max(math.Max(r, g), b)
	minVal := math.Min(math.Min(r, g), b)

	if maxVal == 0 {
		return 0
	}
	return (maxVal - minVal) / maxVal
}

// applyLinearGradientBackground adds a linear gradient background layer
func applyLinearGradientBackground(img *image.RGBA, colors []GradientColor, width, height int) {
	bounds := img.Bounds()
	
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			// Linear gradient from left to right
			ratio := float64(x) / float64(width)
			colorIndex := ratio * float64(len(colors)-1)
			baseIndex := int(colorIndex)
			if baseIndex >= len(colors)-1 {
				baseIndex = len(colors) - 2
			}
			
			localRatio := colorIndex - float64(baseIndex)
			color1 := colors[baseIndex]
			color2 := colors[baseIndex+1]
			
			// Interpolate with very subtle alpha
			alpha := 0.08 * (1.0 - ratio*0.5) // Much more subtle fade from left to right
			blendedColor := interpolateGradientColors(color1, color2, localRatio, alpha)
			
			existingColor := img.RGBAAt(x, y)
			finalColor := blendColors(existingColor, blendedColor, alpha)
			img.SetRGBA(x, y, finalColor)
		}
	}
}

// applyRadialGradientBackground adds a radial gradient background layer
func applyRadialGradientBackground(img *image.RGBA, colors []GradientColor, width, height int, rng *rand.Rand) {
	bounds := img.Bounds()
	centerX := float64(width) * 0.37 // ~150f in 400px equivalent
	centerY := float64(height) * 0.5 // Center vertically
	maxRadius := 120.0 * (float64(width) / 400.0) // Scale to image size
	
	midColorIndex := len(colors) / 2
	firstColor := colors[0]
	lastColor := colors[len(colors)-1]
	midColor := colors[midColorIndex]
	
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			dx := float64(x) - centerX
			dy := float64(y) - centerY
			distance := math.Sqrt(dx*dx + dy*dy)
			
			if distance <= maxRadius {
				ratio := distance / maxRadius
				var targetColor GradientColor
				var alpha float64
				
				if ratio < 0.33 {
					targetColor = midColor
					alpha = 0.06 * (1.0 - ratio*1.5) // Reduced from 0.15
				} else if ratio < 0.66 {
					targetColor = firstColor
					alpha = 0.04 * (1.0 - ratio) // Reduced from 0.08
				} else {
					targetColor = lastColor
					alpha = 0.05 * (1.0 - ratio) // Reduced from 0.12
				}
				
				existingColor := img.RGBAAt(x, y)
				finalColor := blendColors(existingColor, targetColor, alpha)
				img.SetRGBA(x, y, finalColor)
			}
		}
	}
}

// applyDiagonalGradientBackground adds a diagonal accent background layer
func applyDiagonalGradientBackground(img *image.RGBA, colors []GradientColor, width, height int) {
	bounds := img.Bounds()
	lastColor := colors[len(colors)-1]
	firstColor := colors[0]
	
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			// Diagonal gradient from bottom-left to top-right
			diagonalRatio := (float64(x)/float64(width) + (1.0-float64(y)/float64(height))) / 2.0
			
			var targetColor GradientColor
			var alpha float64
			
			if diagonalRatio < 0.2 || diagonalRatio > 0.8 {
				// Transparent areas
				continue
			} else if diagonalRatio < 0.4 {
				targetColor = lastColor
				alpha = 0.1 * (diagonalRatio - 0.2) / 0.2
			} else if diagonalRatio < 0.6 {
				targetColor = firstColor
				alpha = 0.15
			} else {
				targetColor = lastColor
				alpha = 0.1 * (0.8 - diagonalRatio) / 0.2
			}
			
			existingColor := img.RGBAAt(x, y)
			finalColor := blendColors(existingColor, targetColor, alpha)
			img.SetRGBA(x, y, finalColor)
		}
	}
}

// interpolateGradientColors interpolates between two gradient colors
func interpolateGradientColors(c1, c2 GradientColor, ratio, alpha float64) GradientColor {
	invRatio := 1.0 - ratio
	return GradientColor{
		R: uint8(float64(c1.R)*invRatio + float64(c2.R)*ratio),
		G: uint8(float64(c1.G)*invRatio + float64(c2.G)*ratio),
		B: uint8(float64(c1.B)*invRatio + float64(c2.B)*ratio),
	}
}

// createEnhancedOrganicLayers generates organic layers similar to NocturneCompanion
func createEnhancedOrganicLayers(colors []GradientColor, width, height int, rng *rand.Rand) []OrganicLayer {
	var layers []OrganicLayer

	// Create 3-5 layers per color for rich blending (matching companion app)
	for _, baseColor := range colors {
		layerCount := 3 + rng.Intn(3) // 3-5 layers
		for range layerCount {
			// More randomized positioning across the entire area
			centerX := rng.Float64() * float64(width)
			centerY := rng.Float64() * float64(height)

			// More varied radius sizes (40f + random * 180f scaled)
			radius := 40.0*(float64(width)/400.0) + rng.Float64()*180.0*(float64(width)/400.0)

			// More varied but gentler alpha levels with randomization
			alpha := 0.02 + rng.Float64()*0.08 // Reduced from 0.03-0.21 to 0.02-0.10

			// Enhanced gradient patterns with more variety (8 patterns like companion)
			layerType := rng.Intn(8)
			layer := OrganicLayer{
				Colors: []GradientColor{baseColor},
				Center: image.Point{int(centerX), int(centerY)},
				Radius: radius,
				Alpha:  alpha,
				Type:   getLayerType(layerType),
			}

			layers = append(layers, layer)
		}
	}

	// Shuffle layers for more organic look (matching companion app)
	for i := range layers {
		j := rng.Intn(i + 1)
		layers[i], layers[j] = layers[j], layers[i]
	}

	return layers
}

// getLayerType returns the layer type based on pattern number (matching companion app)
func getLayerType(pattern int) string {
	switch pattern {
	case 0:
		return "radial"    // Multi-step radial gradient
	case 1:
		return "linear"    // Asymmetric linear gradient
	case 2:
		return "eccentric" // Eccentric radial gradient
	case 3:
		return "sweep"     // Curved sweep effect
	case 4:
		return "blob"      // Organic blob gradient
	case 5:
		return "streak"    // Diagonal streak
	case 6:
		return "halo"      // Soft halo effect
	case 7:
		return "radial"    // Default radial
	default:
		return "radial"
	}
}

// applyOrganicLayer applies a single organic layer to the image (matching companion app patterns)
func applyOrganicLayer(img *image.RGBA, layer OrganicLayer, width, height int) {
	bounds := img.Bounds()
	baseColor := layer.Colors[0]
	
	// Create deterministic but varied randomization based on position and layer
	seedValue := int64(layer.Center.X + layer.Center.Y*width) + int64(baseColor.R+baseColor.G+baseColor.B)
	rng := rand.New(rand.NewSource(seedValue))

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			var alpha float64

			switch layer.Type {
			case "radial":
				// Multi-step radial gradient (pattern 0, 2, 4, 7)
				dx := float64(x - layer.Center.X)
				dy := float64(y - layer.Center.Y)
				distance := math.Sqrt(dx*dx + dy*dy)
				
				if distance <= layer.Radius {
					ratio := distance / layer.Radius
					if ratio < 0.2 {
						alpha = layer.Alpha * 0.8 // Reduced from 1.2
					} else if ratio < 0.4 {
						alpha = layer.Alpha * (0.8 - ratio*1.4)
					} else if ratio < 0.6 {
						alpha = layer.Alpha * (0.6 - ratio*0.8)
					} else if ratio < 0.8 {
						alpha = layer.Alpha * (0.3 - ratio*0.3)
					} else {
						alpha = layer.Alpha * (0.1 - ratio*0.1)
					}
				}

			case "linear":
				// Asymmetric linear gradient (pattern 1, 3, 5)
				dx := float64(x - layer.Center.X)
				dy := float64(y - layer.Center.Y)
				distance := math.Abs(dx) + math.Abs(dy)*0.5
				
				if distance <= layer.Radius {
					ratio := distance / layer.Radius
					if ratio < 0.3 {
						alpha = layer.Alpha * 0.2 // Reduced from 0.3
					} else if ratio < 0.5 {
						alpha = layer.Alpha * 0.6 // Reduced from 1.0
					} else if ratio < 0.7 {
						alpha = layer.Alpha * 0.4 // Reduced from 0.7
					} else if ratio < 0.9 {
						alpha = layer.Alpha * 0.1 // Reduced from 0.2
					}
				}

			case "sweep":
				// Curved sweep effect (pattern 6)
				dx := float64(x - layer.Center.X)
				dy := float64(y - layer.Center.Y)
				distance := math.Sqrt(dx*dx + dy*dy)
				
				if distance <= layer.Radius && distance > 0 {
					angle := math.Atan2(dy, dx)
					normalizedAngle := (angle + math.Pi) / (2 * math.Pi)
					sweepAlpha := math.Sin(normalizedAngle*math.Pi*3) * math.Sin(normalizedAngle*math.Pi*3)
					distanceRatio := 1.0 - (distance / layer.Radius)
					alpha = layer.Alpha * sweepAlpha * distanceRatio * 0.8
				}

			case "eccentric":
				// Eccentric radial gradient (pattern variations)
				centerOffset := layer.Radius * 0.25
				dx := float64(x - layer.Center.X) + centerOffset
				dy := float64(y - layer.Center.Y)
				distance := math.Sqrt(dx*dx + dy*dy)
				
				if distance <= layer.Radius*1.2 {
					ratio := distance / (layer.Radius * 1.2)
					if ratio < 0.6 {
						alpha = layer.Alpha * (0.9 - ratio*0.3)
					} else {
						alpha = layer.Alpha * (0.6 - ratio*0.4)
					}
				}

			case "halo":
				// Soft halo effect (pattern variations)
				dx := float64(x - layer.Center.X)
				dy := float64(y - layer.Center.Y)
				distance := math.Sqrt(dx*dx + dy*dy)
				
				if distance <= layer.Radius*1.4 {
					ratio := distance / (layer.Radius * 1.4)
					if ratio < 0.2 {
						alpha = layer.Alpha * 0.7
					} else if ratio < 0.4 {
						alpha = layer.Alpha * 0.9
					} else if ratio < 0.6 {
						alpha = layer.Alpha * 0.6
					} else if ratio < 0.8 {
						alpha = layer.Alpha * 0.3
					} else {
						alpha = layer.Alpha * 0.1
					}
				}

			case "streak":
				// Diagonal streak (pattern variations)
				dx := float64(x - layer.Center.X)
				dy := float64(y - layer.Center.Y)
				streakDistance := math.Abs(dx*0.8 + dy*0.3)
				
				if streakDistance <= layer.Radius*0.8 {
					ratio := streakDistance / (layer.Radius * 0.8)
					if ratio < 0.1 {
						alpha = 0
					} else if ratio < 0.3 {
						alpha = layer.Alpha * 0.6
					} else if ratio < 0.5 {
						alpha = layer.Alpha * 1.0
					} else if ratio < 0.7 {
						alpha = layer.Alpha * 0.4
					} else {
						alpha = 0
					}
				}

			case "blob":
				// Organic blob gradient (pattern variations)
				dx := float64(x - layer.Center.X)
				dy := float64(y - layer.Center.Y)
				distance := math.Sqrt(dx*dx + dy*dy)
				
				// Add organic distortion
				distortion := math.Sin(dx*0.02) * math.Cos(dy*0.02) * layer.Radius * 0.2
				adjustedRadius := layer.Radius + distortion
				
				if distance <= adjustedRadius {
					ratio := distance / adjustedRadius
					if ratio < 0.5 {
						alpha = layer.Alpha * 1.1
					} else {
						alpha = layer.Alpha * (1.1 - ratio*1.6)
					}
				}

			default:
				// Default radial fallback
				dx := float64(x - layer.Center.X)
				dy := float64(y - layer.Center.Y)
				distance := math.Sqrt(dx*dx + dy*dy)
				
				if distance <= layer.Radius {
					ratio := distance / layer.Radius
					alpha = layer.Alpha * (1.0 - ratio)
				}
			}

			if alpha > 0 && alpha <= 1.0 {
				// Add randomization to alpha for more organic appearance
				randomVariation := 0.5 + rng.Float64()*0.5 // 0.5-1.0 multiplier
				finalAlpha := alpha * randomVariation
				
				// Ensure alpha doesn't get too strong
				if finalAlpha > 0.12 {
					finalAlpha = 0.12
				}
				
				// Apply the layer color with calculated alpha
				existingColor := img.RGBAAt(x, y)
				newColor := blendColors(existingColor, baseColor, finalAlpha)
				img.SetRGBA(x, y, newColor)
			}
		}
	}
}

// blendColors blends a new color over existing color with alpha
func blendColors(existing color.RGBA, newColor GradientColor, alpha float64) color.RGBA {
	if alpha <= 0 {
		return existing
	}
	if alpha >= 1 {
		return color.RGBA{newColor.R, newColor.G, newColor.B, 255}
	}

	// Alpha blending
	invAlpha := 1.0 - alpha
	blendedR := uint8(float64(existing.R)*invAlpha + float64(newColor.R)*alpha)
	blendedG := uint8(float64(existing.G)*invAlpha + float64(newColor.G)*alpha)
	blendedB := uint8(float64(existing.B)*invAlpha + float64(newColor.B)*alpha)

	return color.RGBA{blendedR, blendedG, blendedB, 255}
}

// SaveGradientImage saves the gradient image to the specified path
func SaveGradientImage(img *image.RGBA, filePath string) error {
	// Ensure directory exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %v", err)
	}
	defer file.Close()

	if err := png.Encode(file, img); err != nil {
		return fmt.Errorf("failed to encode PNG: %v", err)
	}

	return nil
}

// applyGaussianBlur applies a Gaussian blur to the image for softer appearance
func applyGaussianBlur(img *image.RGBA, radius float64) *image.RGBA {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	blurred := image.NewRGBA(bounds)

	// Generate Gaussian kernel
	kernelSize := int(radius*2)*2 + 1
	kernel := make([]float64, kernelSize)
	kernelSum := 0.0
	
	for i := 0; i < kernelSize; i++ {
		x := float64(i - kernelSize/2)
		kernel[i] = math.Exp(-(x*x) / (2*radius*radius))
		kernelSum += kernel[i]
	}
	
	// Normalize kernel
	for i := range kernel {
		kernel[i] /= kernelSum
	}

	// Horizontal pass
	temp := image.NewRGBA(bounds)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var r, g, b, a float64
			
			for i := 0; i < kernelSize; i++ {
				xi := x + i - kernelSize/2
				if xi < 0 {
					xi = 0
				} else if xi >= width {
					xi = width - 1
				}
				
				pixel := img.RGBAAt(xi, y)
				weight := kernel[i]
				
				r += float64(pixel.R) * weight
				g += float64(pixel.G) * weight
				b += float64(pixel.B) * weight
				a += float64(pixel.A) * weight
			}
			
			temp.SetRGBA(x, y, color.RGBA{
				R: uint8(r + 0.5),
				G: uint8(g + 0.5),
				B: uint8(b + 0.5),
				A: uint8(a + 0.5),
			})
		}
	}

	// Vertical pass
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var r, g, b, a float64
			
			for i := 0; i < kernelSize; i++ {
				yi := y + i - kernelSize/2
				if yi < 0 {
					yi = 0
				} else if yi >= height {
					yi = height - 1
				}
				
				pixel := temp.RGBAAt(x, yi)
				weight := kernel[i]
				
				r += float64(pixel.R) * weight
				g += float64(pixel.G) * weight
				b += float64(pixel.B) * weight
				a += float64(pixel.A) * weight
			}
			
			blurred.SetRGBA(x, y, color.RGBA{
				R: uint8(r + 0.5),
				G: uint8(g + 0.5),
				B: uint8(b + 0.5),
				A: uint8(a + 0.5),
			})
		}
	}

	return blurred
}

// ProcessGradientColors handles incoming gradient colors and generates the image
func ProcessGradientColors(payload []byte) error {
	log.Printf("Processing gradient colors payload (%d bytes)", len(payload))

	// Parse the gradient colors
	colors, err := ParseGradientColorsPayload(payload)
	if err != nil {
		return fmt.Errorf("failed to parse gradient colors: %v", err)
	}

	log.Printf("Parsed %d gradient colors", len(colors))

	// Generate the gradient image (800x400 for good quality)
	img, err := GenerateLayeredGradientImage(colors, 800, 400)
	if err != nil {
		return fmt.Errorf("failed to generate gradient image: %v", err)
	}

	// Create filename with timestamp
	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("gradient_%s.png", timestamp)
	gradientDir := "/var/nocturne/gradients"
	fullPath := filepath.Join(gradientDir, filename)

	// Save the image
	if err := SaveGradientImage(img, fullPath); err != nil {
		return fmt.Errorf("failed to save gradient image: %v", err)
	}

	log.Printf("Generated blurred gradient image saved to: %s", fullPath)
	return nil
}