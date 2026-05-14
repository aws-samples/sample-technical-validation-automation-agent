package validator

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// makePNG produces a w×h PNG with a 4-color checkerboard so the codec
// has actual content to compress.
func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	colors := []color.RGBA{
		{255, 0, 0, 255},
		{0, 255, 0, 255},
		{0, 0, 255, 255},
		{255, 255, 0, 255},
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, colors[(x+y)%4])
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestResizeIfNeeded_SmallImagePassesThrough(t *testing.T) {
	src := makePNG(t, 256, 256)
	out, err := resizeIfNeeded(src, "png", "/tmp/x.png", newImageCache())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, src) {
		t.Error("small image should pass through unchanged")
	}
}

func TestResizeIfNeeded_LargeImageDownscales(t *testing.T) {
	// MaxImageDim is 8000 — anything bigger triggers resize. Use a
	// modest 8200×400 source so the test stays fast.
	src := makePNG(t, 8200, 400)
	out, err := resizeIfNeeded(src, "png", "/tmp/large.png", newImageCache())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(out, src) {
		t.Fatal("expected resized output to differ from source")
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	if b.Dx() > MaxImageDim || b.Dy() > MaxImageDim {
		t.Errorf("resized dims = %dx%d, both must be <= %d", b.Dx(), b.Dy(), MaxImageDim)
	}
}

// B13 (ported as-is): cache hit returns the previously stored payload
// keyed on the path string, even if the bytes change underneath.
func TestResizeIfNeeded_CacheHitReturnsPriorBytes_B13(t *testing.T) {
	cache := newImageCache()
	cache.put("/p", []byte("prior"))
	out, err := resizeIfNeeded(makePNG(t, 200, 200), "png", "/p", cache)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "prior" {
		t.Errorf("cache-hit returned %q, want %q (path-string cache key)", string(out), "prior")
	}
}

func TestResizeIfNeeded_CachePopulatedOnPassthrough(t *testing.T) {
	cache := newImageCache()
	src := makePNG(t, 32, 32)
	if _, err := resizeIfNeeded(src, "png", "/q", cache); err != nil {
		t.Fatal(err)
	}
	cached, ok := cache.get("/q")
	if !ok || !bytes.Equal(cached, src) {
		t.Error("expected cache to hold the source bytes after passthrough")
	}
}

func TestResizeIfNeeded_DecodeFailureReturnsSrcUnchanged(t *testing.T) {
	src := []byte("not really a png")
	out, err := resizeIfNeeded(src, "png", "", nil)
	if err != nil {
		t.Fatalf("decode failure should be silent (caller will let Bedrock report); got %v", err)
	}
	if !bytes.Equal(out, src) {
		t.Error("decode failure should pass src through unchanged")
	}
}

func TestScaledDims(t *testing.T) {
	cases := []struct {
		w, h  int
		expW  int
		expH  int
		descr string
	}{
		{10000, 5000, 8000, 4000, "wider"},
		{5000, 10000, 4000, 8000, "taller"},
		{8000, 8000, 8000, 8000, "exactly at limit"},
	}
	for _, c := range cases {
		gotW, gotH := scaledDims(c.w, c.h, MaxImageDim)
		if gotW != c.expW || gotH != c.expH {
			t.Errorf("%s: scaledDims(%d,%d) = (%d,%d), want (%d,%d)",
				c.descr, c.w, c.h, gotW, gotH, c.expW, c.expH)
		}
	}
}
