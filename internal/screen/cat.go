package screen

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"log"
	"math"
	"net/http"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
)

// DefaultCatURL fetches a random cat without an API key, pre-desaturated via
// cataas's custom filter and sized 1000x750 (4:3, matching the panel) so it
// scales up to fill the screen. cataas caps output at 1000x1000.
const DefaultCatURL = "https://cataas.com/cat?filter=custom&r=100&g=100&b=100&width=1000&height=750"

// maxCatBody caps how much image data a single fetch will read.
const maxCatBody = 20 << 20

// Cat renders a random cat photo fetched over HTTP, scaled to fill the panel.
// The last successfully fetched photo is kept as a fallback so a flaky source
// doesn't blank the screen.
type Cat struct {
	url  string
	http *http.Client

	mu   sync.Mutex
	last image.Image
}

// NewCat creates the cat screen. An empty url uses DefaultCatURL.
func NewCat(url string) *Cat {
	if url == "" {
		url = DefaultCatURL
	}
	return &Cat{url: url, http: &http.Client{Timeout: 15 * time.Second}}
}

// Name implements Screen.
func (c *Cat) Name() string { return "cat" }

// Render implements Screen.
func (c *Cat) Render(ctx context.Context, now time.Time, width, height int) ([]byte, error) {
	img, err := c.fetch(ctx)
	if err != nil {
		c.mu.Lock()
		img = c.last
		c.mu.Unlock()
		if img == nil {
			return nil, fmt.Errorf("fetch cat (no cached fallback): %w", err)
		}
		log.Printf("cat fetch failed, reusing cached image: %v", err)
	} else {
		c.mu.Lock()
		c.last = img
		c.mu.Unlock()
	}
	return fit(img, width, height)
}

func (c *Cat) fetch(ctx context.Context) (image.Image, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cat source: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCatBody))
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("decode cat image: %w", err)
	}
	return img, nil
}

// maxEInkImageBytes stays just under the TRMNL X firmware's 750000-byte image
// limit (config.h: MAX_IMAGE_SIZE); a larger download is rejected by the device.
const maxEInkImageBytes = 745000

// grayLevels are the grayscale depths fit tries, richest first. The X is a true
// 16-level panel (FastEPD 4-bpp mode), so 16 is the quality target; busy frames
// that would exceed the size cap step down to 8 then 4 levels (4 always fits).
// All encode to a compact <=4-bpp PNG, and grayscale always refreshes fully.
var grayLevels = []int{16, 8, 4}

// grayPalette returns an evenly spaced n-level grayscale palette.
func grayPalette(n int) color.Palette {
	p := make(color.Palette, n)
	for i := range p {
		p[i] = color.Gray{Y: uint8(i * 0xFF / (n - 1))}
	}
	return p
}

// fit scales img to completely fill a width x height canvas (cover: upscaling as
// needed and center-cropping any overflow), then Floyd-Steinberg dithers it to
// the most gray levels that keep the PNG under the device's size limit.
func fit(img image.Image, width, height int) ([]byte, error) {
	canvas := image.NewGray(image.Rect(0, 0, width, height))
	xdraw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, xdraw.Src)

	b := img.Bounds()
	if b.Dx() > 0 && b.Dy() > 0 {
		// Cover: scale so the image fills both dimensions, center it, and let the
		// draw clip whatever extends past the canvas.
		scale := max(float64(width)/float64(b.Dx()), float64(height)/float64(b.Dy()))
		w := int(math.Ceil(float64(b.Dx()) * scale))
		h := int(math.Ceil(float64(b.Dy()) * scale))
		x0 := (width - w) / 2
		y0 := (height - h) / 2
		xdraw.CatmullRom.Scale(canvas, image.Rect(x0, y0, x0+w, y0+h), img, b, xdraw.Over, nil)
	}

	var smallest []byte
	for _, n := range grayLevels {
		dithered := image.NewPaletted(canvas.Bounds(), grayPalette(n))
		draw.FloydSteinberg.Draw(dithered, dithered.Bounds(), canvas, image.Point{})
		var buf bytes.Buffer
		if err := png.Encode(&buf, dithered); err != nil {
			return nil, err
		}
		if buf.Len() <= maxEInkImageBytes {
			return buf.Bytes(), nil
		}
		smallest = buf.Bytes()
	}
	// Even the fewest-level encoding exceeded the cap (very unlikely); send it.
	return smallest, nil
}
