package brand

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/appdir"
)

func reset(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	mu.Lock()
	current = Default()
	loaded = make(chan struct{})
	loadOnce = sync.Once{}
	mu.Unlock()
}

func TestDefaultIsUpstream(t *testing.T) {
	reset(t)
	if Name() != "magpie" || Site() != "https://usemagpie.ai" {
		t.Fatalf("default brand = %q at %q", Name(), Site())
	}
	if AssetPrefix() != "magpie" {
		t.Fatalf("default asset prefix = %q", AssetPrefix())
	}
	if Feed() != "https://usemagpie.ai/api/latest" {
		t.Fatalf("default feed = %q", Feed())
	}
	if SignIn() != nil || Relay() != nil || Guide() != nil {
		t.Fatal("default config has a sign-in, relay or guide")
	}
}

func TestFetchSetsAndKeeps(t *testing.T) {
	reset(t)
	cfg := Config{
		Schema: 1,
		Brand: Brand{
			Name:      "Acme Desktop",
			ShortName: "Acme",
			Site:      "https://acme.example",
			Icon:      "https://acme.example/icon.png",
		},
		Updates:  Updates{Feed: "https://acme.example/api/latest", AssetPrefix: "acme"},
		Partners: "https://acme.example/api/partners",
		Account: &Account{
			BaseURL:  "https://acme.example",
			Login:    "/api/v1/auth/login",
			ListKeys: "/api/v1/keys",
		},
		Provider: &Provider{ID: "acme", Name: "Acme Relay", Chat: "https://api.acme.example/v1"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(cfg)
	}))
	defer srv.Close()

	t.Setenv("MAGPIE_BRAND_CONFIG", srv.URL)
	Load(context.Background(), 5*time.Second)

	if Name() != "Acme Desktop" || ShortName() != "Acme" {
		t.Fatalf("brand = %q / %q", Name(), ShortName())
	}
	if Site() != "https://acme.example" || Icon() == "" {
		t.Fatalf("site = %q icon = %q", Site(), Icon())
	}
	if AssetPrefix() != "acme" || Feed() != "https://acme.example/api/latest" {
		t.Fatalf("prefix = %q feed = %q", AssetPrefix(), Feed())
	}
	if SignIn() == nil || SignIn().Login != "/api/v1/auth/login" {
		t.Fatalf("sign-in = %+v", SignIn())
	}
	if Relay() == nil || Relay().ID != "acme" {
		t.Fatalf("relay = %+v", Relay())
	}

	// The kept file carries the same config into a later run.
	if _, err := os.Stat(filepath.Join(appdir.Cache(), "brand.json")); err != nil {
		t.Fatalf("cache not written: %v", err)
	}
	mu.Lock()
	current = Default()
	loaded = make(chan struct{})
	loadOnce = sync.Once{}
	mu.Unlock()
	readCache()
	if Name() != "Acme Desktop" {
		t.Fatalf("from cache: %q", Name())
	}
}

func TestMergeFillsEmptyFields(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	merged := merge(Config{Brand: Brand{Name: "Acme"}})
	if merged.Brand.ShortName != "Acme" {
		t.Fatalf("short name = %q", merged.Brand.ShortName)
	}
	if merged.Updates.Feed == "" || merged.Partners == "" {
		t.Fatalf("empty fields not filled: %+v", merged)
	}
	if merged.Brand.Site == "" {
		t.Fatal("site not filled")
	}
}

func TestBrokenConfigKeepsDefault(t *testing.T) {
	reset(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("MAGPIE_BRAND_CONFIG", srv.URL)
	Load(context.Background(), 5*time.Second)
	if Name() != "magpie" {
		t.Fatalf("name after a failed fetch = %q", Name())
	}
}

func TestOffKeepsDefault(t *testing.T) {
	reset(t)
	t.Setenv("MAGPIE_BRAND_CONFIG", "off")
	Load(context.Background(), 5*time.Second)
	if Name() != "magpie" {
		t.Fatalf("name with off = %q", Name())
	}
}
