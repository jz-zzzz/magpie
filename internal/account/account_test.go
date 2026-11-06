package account

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yetone/magpie/internal/brand"
)

func TestResolve(t *testing.T) {
	c := New(brand.Account{BaseURL: "https://api.example/v1/"})
	for _, tc := range []struct{ in, want string }{
		{"/api/v1/auth/login", "https://api.example/api/v1/auth/login"},
		{"auth/login", "https://api.example/v1/auth/login"},
		{"https://other.example/x", "https://other.example/x"},
	} {
		got, err := c.resolve(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("resolve(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestLoginEnvelope(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["email"] != "a@b.c" || body["password"] != "pw" {
			t.Errorf("body = %v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"data": map[string]any{"token": "tok-1", "email": "a@b.c"},
		})
	}))
	defer srv.Close()

	c := New(brand.Account{BaseURL: srv.URL, Login: "/auth/login"})
	s, err := c.Login(context.Background(), "a@b.c", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if s.Token != "tok-1" || s.User != "a@b.c" {
		t.Fatalf("session = %+v", s)
	}
	if gotPath != "/auth/login" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "" {
		t.Fatalf("login carried a token: %q", gotAuth)
	}
	if !c.SignedIn() {
		t.Fatal("not signed in after login")
	}
}

func TestKeysPlainAndBoxed(t *testing.T) {
	for _, body := range []string{
		`[{"id":1,"name":"main","key":"sk-1"}]`,
		`{"code":200,"data":[{"id":1,"name":"main","key":"sk-1"}]}`,
		`{"keys":[{"id":1,"name":"main","key":"sk-1"}]}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		c := New(brand.Account{BaseURL: srv.URL, ListKeys: "/keys"})
		keys, err := c.Keys(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if len(keys) != 1 || keys[0].Key != "sk-1" || keys[0].Name != "main" {
			t.Fatalf("%s: keys = %+v", body, keys)
		}
		srv.Close()
	}
}

func TestEnsureKeyCreatesWhenNone(t *testing.T) {
	created := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
		case r.Method == "POST" && r.URL.Path == "/keys":
			created = true
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": 7, "name": "magpie", "key": "sk-new"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := New(brand.Account{BaseURL: srv.URL, ListKeys: "/keys", CreateKey: "/keys"})
	k, err := c.EnsureKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !created || k.Key != "sk-new" {
		t.Fatalf("created = %v, key = %+v", created, k)
	}
}

func TestEnsureKeyReturnsFirst(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "name": "first", "key": "sk-1"}, {"id": 2, "name": "second", "key": "sk-2"}})
	}))
	defer srv.Close()

	c := New(brand.Account{BaseURL: srv.URL, ListKeys: "/keys"})
	k, err := c.EnsureKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if k.Key != "sk-1" {
		t.Fatalf("key = %+v", k)
	}
}

func TestUnauthorizedSaysSignIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(brand.Account{BaseURL: srv.URL, ListKeys: "/keys"})
	if _, err := c.Keys(context.Background()); err == nil {
		t.Fatal("no error on 401")
	} else if got := err.Error(); got == "" || !contains(got, "sign in again") {
		t.Fatalf("error = %q", got)
	}
}

func TestNoRouteSaysSo(t *testing.T) {
	c := New(brand.Account{BaseURL: "https://api.example"})
	if _, err := c.Login(context.Background(), "a", "b"); err == nil {
		t.Fatal("login worked with no route")
	}
	if _, err := c.Keys(context.Background()); err == nil {
		t.Fatal("keys worked with no route")
	}
}

func TestCustomSessionFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-2", "username": "someone"})
	}))
	defer srv.Close()
	c := New(brand.Account{
		BaseURL: srv.URL,
		Login:   "/login",
		Session: brand.Session{Token: "access_token", User: "username"},
	})
	s, err := c.Login(context.Background(), "a@b.c", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if s.Token != "tok-2" || s.User != "someone" {
		t.Fatalf("session = %+v", s)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || (len(s) > 0 && (stringIndex(s, sub) >= 0)))
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
