package brand

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// testPNG is a 4x4 picture, encoded as a PNG the way a site serves one.
func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetNRGBA(x, y, color.NRGBA{0x20, 0x60, 0xc0, 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestIconFetchedAndKept(t *testing.T) {
	reset(t)
	icon := testPNG(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(icon)
	}))
	defer srv.Close()

	if IconBytes() != nil {
		t.Fatal("an icon before any was fetched")
	}
	if err := fetchIcon(context.Background(), srv.URL+"/icon.png"); err != nil {
		t.Fatal(err)
	}
	got := IconBytes()
	if !bytes.Equal(got, icon) {
		t.Fatalf("icon = %d bytes, want %d", len(got), len(icon))
	}
	if iconWanted(srv.URL + "/icon.png") {
		t.Fatal("the same icon is wanted again")
	}
	if !iconWanted(srv.URL + "/other.png") {
		t.Fatal("another icon is not wanted")
	}
}

func TestIconNotAPNG(t *testing.T) {
	reset(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>not a picture</html>"))
	}))
	defer srv.Close()
	if err := fetchIcon(context.Background(), srv.URL+"/icon.png"); err == nil {
		t.Fatal("a page was kept as an icon")
	}
	if IconBytes() != nil {
		t.Fatal("an icon after a refused fetch")
	}
}

func TestIconUnreadableKept(t *testing.T) {
	reset(t)
	if err := os.MkdirAll(cacheDir(t), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(iconFile(), []byte("not a png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if IconBytes() != nil {
		t.Fatal("a broken icon was handed out")
	}
}

func cacheDir(t *testing.T) string {
	t.Helper()
	f := iconFile()
	return f[:len(f)-len("/brand-icon.png")]
}
