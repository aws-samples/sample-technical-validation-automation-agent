package validator

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"strings"
	"sync"

	"golang.org/x/image/draw"
)

// MaxImageDim is Bedrock's per-dimension cap. Mirrors the Python
// `_resize_image_if_needed` constant max_dim = 8000.
const MaxImageDim = 8000

// jpegResizeQuality matches the Python `img.save(... quality=95)` setting.
const jpegResizeQuality = 95

// imageCache memoises resized image bytes keyed on the absolute path
// supplied by the caller.
//
// Audit B13 ported as-is: the cache key is the path string, not the file
// content. Symlinks and relative paths can cause re-resize; accepted
// tradeoff per PLAN.md (partner folders rarely exhibit this).
type imageCache struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newImageCache() *imageCache { return &imageCache{m: make(map[string][]byte)} }

func (c *imageCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[key]
	return v, ok
}

func (c *imageCache) put(key string, v []byte) {
	c.mu.Lock()
	c.m[key] = v
	c.mu.Unlock()
}

// resizeIfNeeded decodes the supplied image bytes and downscales them to
// fit within MaxImageDim per dimension, preserving aspect ratio. The
// returned bytes are guaranteed to be non-zero; if decoding fails or no
// resize is required, src is returned unchanged.
//
// ext is the canonical Bedrock format token: "jpeg" or "png" (callers
// must map ".jpg" → "jpeg" before invoking).
//
// If cache != nil, results are memoised under cacheKey (typically an
// absolute path).
func resizeIfNeeded(src []byte, ext, cacheKey string, cache *imageCache) ([]byte, error) {
	if cache != nil && cacheKey != "" {
		if v, ok := cache.get(cacheKey); ok {
			return v, nil
		}
	}

	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		// Decode failure is non-fatal — the caller will pass src bytes
		// straight to Bedrock and let the API report the issue.
		if cache != nil && cacheKey != "" {
			cache.put(cacheKey, src)
		}
		return src, nil //nolint:nilerr // intentional: degrade gracefully
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= MaxImageDim && h <= MaxImageDim {
		if cache != nil && cacheKey != "" {
			cache.put(cacheKey, src)
		}
		return src, nil
	}

	newW, newH := scaledDims(w, h, MaxImageDim)
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	// CatmullRom is the closest stdlib-friendly equivalent to Pillow's
	// Lanczos resampler. Matches PLAN.md decision for image/draw.
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)

	out, err := encodeImage(dst, ext)
	if err != nil {
		return nil, err
	}
	if cache != nil && cacheKey != "" {
		cache.put(cacheKey, out)
	}
	return out, nil
}

// scaledDims returns new (width, height) constrained so the larger
// dimension equals limit, preserving aspect ratio. Mirrors the Python
// arithmetic.
func scaledDims(w, h, limit int) (int, int) {
	if w >= h {
		nw := limit
		nh := int(float64(h) * (float64(limit) / float64(w)))
		if nh < 1 {
			nh = 1
		}
		return nw, nh
	}
	nh := limit
	nw := int(float64(w) * (float64(limit) / float64(h)))
	if nw < 1 {
		nw = 1
	}
	return nw, nh
}

func encodeImage(img image.Image, ext string) ([]byte, error) {
	var buf bytes.Buffer
	switch strings.ToLower(ext) {
	case "jpeg", "jpg":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegResizeQuality}); err != nil {
			return nil, fmt.Errorf("validator: jpeg encode: %w", err)
		}
	case "png":
		if err := png.Encode(&buf, img); err != nil {
			return nil, fmt.Errorf("validator: png encode: %w", err)
		}
	default:
		return nil, errors.New("validator: unsupported image format " + ext)
	}
	return buf.Bytes(), nil
}
