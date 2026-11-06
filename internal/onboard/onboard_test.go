package onboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/yetone/magpie/internal/brand"
)

func TestOpenNilWithoutSignIn(t *testing.T) {
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
