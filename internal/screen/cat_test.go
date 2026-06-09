package screen

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// catSource serves a small generated photo, or 500s when failing is set.
func catSource(t *testing.T, failing *atomic.Bool) *httptest.Server {
	t.Helper()
	src := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for y := 0; y < 30; y++ {
		for x := 0; x < 40; x++ {
			src.Set(x, y, color.RGBA{uint8(x * 6), uint8(y * 8), 0, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(buf.Bytes())
	}))
}

func TestCatRendersPanelSizedPNG(t *testing.T) {
	var failing atomic.Bool
	srv := catSource(t, &failing)
	defer srv.Close()

	c := NewCat(srv.URL)
	out, err := c.Render(context.Background(), time.Now(), 200, 100)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("output is not a PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 200 || b.Dy() != 100 {
		t.Fatalf("output bounds = %v, want 200x100", b)
	}
}

func TestCatFallsBackToCachedImage(t *testing.T) {
	var failing atomic.Bool
	srv := catSource(t, &failing)
	defer srv.Close()

	c := NewCat(srv.URL)
	if _, err := c.Render(context.Background(), time.Now(), 200, 100); err != nil {
		t.Fatalf("first Render: %v", err)
	}

	failing.Store(true)
	out, err := c.Render(context.Background(), time.Now(), 200, 100)
	if err != nil {
		t.Fatalf("Render with failing source should reuse cache, got: %v", err)
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("cached render is not a PNG: %v", err)
	}
}

func TestCatErrorsWithoutCache(t *testing.T) {
	var failing atomic.Bool
	failing.Store(true)
	srv := catSource(t, &failing)
	defer srv.Close()

	c := NewCat(srv.URL)
	if _, err := c.Render(context.Background(), time.Now(), 200, 100); err == nil {
		t.Fatal("expected error when the source fails and nothing is cached")
	}
}
