package screen

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
)

// DefaultCatURL serves a random cat picture without an API key.
const DefaultCatURL = "https://cataas.com/cat"

// maxCatBody caps how much image data a single fetch will read.
const maxCatBody = 20 << 20

// Cat renders a random cat photo fetched over HTTP, letterboxed onto a white
// panel. The last successfully fetched photo is kept as a fallback so a flaky
// source doesn't blank the screen.
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

// fit scales img to fill as much of a width x height white canvas as possible
// without cropping, converts to grayscale, and encodes a PNG.
func fit(img image.Image, width, height int) ([]byte, error) {
	dst := image.NewGray(image.Rect(0, 0, width, height))
	xdraw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, xdraw.Src)

	b := img.Bounds()
	if b.Dx() > 0 && b.Dy() > 0 {
		scale := min(float64(width)/float64(b.Dx()), float64(height)/float64(b.Dy()))
		w := int(float64(b.Dx()) * scale)
		h := int(float64(b.Dy()) * scale)
		x0 := (width - w) / 2
		y0 := (height - h) / 2
		xdraw.CatmullRom.Scale(dst, image.Rect(x0, y0, x0+w, y0+h), img, b, xdraw.Over, nil)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
