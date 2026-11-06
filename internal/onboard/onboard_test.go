package onboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/brand"
	"github.com/yetone/magpie/internal/provider"
)

func TestOpenNilWithoutSignIn(t *testing.T) {
	brand.Reset()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MAGPIE_BRAND_CONFIG", "off")
	if r := Open(); r != nil {
		t.Fatalf("Open with no config = %+v", r)
	}
	if _, err := Ensure(); err == nil {
		t.Fatal("Ensure worked with no config")
	}
}

func TestSignInAndAddRelay(t *testing.T) {
	brand.Reset()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	var signedIn, addedKey bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/login":
			signedIn = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"data": map[string]any{"token": "tok", "email": "a@b.c"},
			})
		case "/keys":
			if r.Method == "POST" {
				addedKey = true
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": 1, "name": "main", "key": "sk-1"}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := brand.Config{
		Brand: brand.Brand{Name: "Acme", ShortName: "Acme"},
		Account: &brand.Account{
			BaseURL:   srv.URL,
			Login:     "/auth/login",
			ListKeys:  "/keys",
			CreateKey: "/keys",
		},
		Provider: &brand.Provider{
			ID:   "acme",
			Name: "Acme Relay",
			Chat: srv.URL + "/v1",
		},
	}
	b, _ := json.Marshal(cfg)
	path := t.TempDir() + "/config.json"
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAGPIE_BRAND_CONFIG", path)
	brand.Load(context.Background(), 5e9)

	r := Open()
	if r == nil {
		t.Fatal("Open with a config = nil")
	}
	if st := r.Status(); !st.HasSignIn || st.SignedIn {
		t.Fatalf("status before sign-in = %+v", st)
	}
	if err := r.SignIn(context.Background(), "a@b.c", "pw"); err != nil {
		t.Fatal(err)
	}
	if !signedIn {
		t.Fatal("login route not asked")
	}
	id, err := r.AddRelay(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id == "" || !addedKey {
		t.Fatalf("id = %q, key created = %v", id, addedKey)
	}
	st := r.Status()
	if !st.SignedIn || st.Provider != id || st.Done {
		t.Fatalf("status after relay = %+v", st)
	}
	// the provider is really there
	if _, err := r.AddRelay(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Finish(); err != nil {
		t.Fatal(err)
	}
	if !r.Status().Done {
		t.Fatal("Finish did not mark done")
	}
}

func TestAddRelayMakesLinesAndGroup(t *testing.T) {
	brand.Reset()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"token": "tok", "email": "a@b.c"}})
		case "/keys":
			if r.Method == "POST" {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": 1, "name": "main", "key": "sk-1"}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
		case "/v1/models", "/hk/v1/models", "/us/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
				{"id": "acme-large", "object": "model"},
				{"id": "acme-small", "object": "model"},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := brand.Config{
		Brand:   brand.Brand{Name: "Acme", ShortName: "Acme"},
		Account: &brand.Account{BaseURL: srv.URL, Login: "/auth/login", ListKeys: "/keys", CreateKey: "/keys"},
		Provider: &brand.Provider{
			ID:   "acme",
			Name: "Acme Relay",
			Chat: srv.URL + "/v1",
			Lines: []brand.Line{
				{Name: "HK", Chat: srv.URL + "/hk/v1"},
				{Name: "US", Chat: srv.URL + "/us/v1"},
			},
		},
	}
	b, _ := json.Marshal(cfg)
	path := t.TempDir() + "/config.json"
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAGPIE_BRAND_CONFIG", path)
	brand.Load(context.Background(), 5e9)

	r := Open()
	if r == nil {
		t.Fatal("Open = nil")
	}
	if err := r.SignIn(context.Background(), "a@b.c", "pw"); err != nil {
		t.Fatal(err)
	}
	mainID, err := r.AddRelay(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Status().Group; got == "" {
		t.Fatal("no group over the lines")
	}
	// the main provider and one per line
	lines := 0
	for _, p := range provider.All() {
		if p.ID == mainID || strings.Contains(p.Name, "·") {
			lines++
		}
	}
	if lines < 3 {
		t.Fatalf("providers from the relay = %d, want the main one and two lines", lines)
	}
	// the group's members: each model on each line, lines adjacent
	g, members, ok := provider.FindGroup("group/" + mainID + "-lines")
	if !ok || len(members) == 0 {
		t.Fatalf("group %s = %+v, %v", mainID+"-lines", g, ok)
	}
	if len(members) != 6 {
		t.Fatalf("members = %d, want 2 models on 3 lines", len(members))
	}
}

func TestLinesProbe(t *testing.T) {
	brand.Reset()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // a line answers, its key refused
	}))
	defer srv.Close()

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close() // nothing listens here now

	cfg := brand.Config{
		Brand:   brand.Brand{Name: "Acme", ShortName: "Acme"},
		Account: &brand.Account{BaseURL: srv.URL, Login: "/login"},
		Provider: &brand.Provider{
			ID:   "acme",
			Name: "Acme Relay",
			Chat: srv.URL + "/v1",
			Lines: []brand.Line{
				{Name: "HK", Chat: srv.URL + "/hk/v1"},
				{Name: "US", Chat: deadURL + "/v1"},
			},
		},
	}
	b, _ := json.Marshal(cfg)
	path := t.TempDir() + "/config.json"
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAGPIE_BRAND_CONFIG", path)
	brand.Load(context.Background(), 5e9)

	r := Open()
	if r == nil {
		t.Fatal("Open = nil")
	}
	lines := r.Lines(context.Background(), false)
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want the main one and two", len(lines))
	}
	if lines[0].Probed {
		t.Fatal("a line was probed without asking")
	}
	probed := r.Lines(context.Background(), true)
	if !probed[0].Probed || !probed[0].OK || probed[0].Latency < 0 {
		t.Fatalf("main line = %+v", probed[0])
	}
	if probed[1].OK == false && probed[1].Error == "" {
		t.Fatalf("a line with no answer said nothing: %+v", probed[1])
	}
	if probed[2].OK {
		t.Fatalf("a line nothing listens on answered: %+v", probed[2])
	}
	if probed[2].Error == "" {
		t.Fatal("a dead line's error is empty")
	}
}
