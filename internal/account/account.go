// Package account signs a user in to the relay this build belongs to, and
// lends the API key its client configures agents with. Nothing about a
// server is compiled in: every route and every field a reply carries comes
// from the brand config (brand.Account), so a deployment that names other
// paths than sub2api's still works.
//
// The account API is optional. A build whose brand config has none keeps
// magpie's own flow: a key pasted by hand.
package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/brand"
	"github.com/yetone/magpie/internal/source"
)

// Session is a signed-in user.
type Session struct {
	Token string `json:"token"`
	User  string `json:"user,omitempty"`
}

// Key is one API key of the account.
type Key struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Key   string `json:"key"`
	Group string `json:"group,omitempty"`
}

// Group is a model group a key may be made in.
type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Client talks to the sign-in API the brand config names.
type Client struct {
	api  brand.Account
	http *http.Client
	mu   sync.Mutex
	sess Session
}

// New is a client for one brand config, with no session yet.
func New(api brand.Account) *Client {
	return &Client{api: api, http: &http.Client{Timeout: 30 * time.Second}}
}

// Enabled is whether this build has a sign-in API at all.
func Enabled() bool {
	api := brand.SignIn()
	return api != nil && strings.TrimSpace(api.BaseURL) != ""
}

// Open is the build's client, restored from the kept session, or nil when
// the build has no sign-in API.
func Open() *Client {
	api := brand.SignIn()
	if api == nil || strings.TrimSpace(api.BaseURL) == "" {
		return nil
	}
	c := New(*api)
	c.restore()
	return c
}

// SignedIn is whether a session is held (restored or made now).
func (c *Client) SignedIn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sess.Token != ""
}

// Session is the session held, if any.
func (c *Client) Session() Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sess
}

// SignOut forgets the session, on disk too.
func (c *Client) SignOut() {
	c.mu.Lock()
	c.sess = Session{}
	c.mu.Unlock()
	_ = os.Remove(sessionFile())
}

// Login signs in with an email and password.
func (c *Client) Login(ctx context.Context, email, password string) (Session, error) {
	if c.api.Login == "" {
		return Session{}, errors.New("this build's account API has no login route")
	}
	var out map[string]any
	body := map[string]any{"email": email, "password": password}
	if err := c.do(ctx, "POST", c.api.Login, body, &out); err != nil {
		return Session{}, err
	}
	s := c.readSession(out)
	if s.Token == "" {
		return Session{}, errors.New("the sign-in answered no token")
	}
	c.keep(s)
	return s, nil
}

// Register creates an account, with the verification code when the server
// asks for one.
func (c *Client) Register(ctx context.Context, email, password, code string) (Session, error) {
	if c.api.Register == "" {
		return Session{}, errors.New("this build's account API has no register route")
	}
	body := map[string]any{"email": email, "password": password}
	if code != "" {
		body["code"] = code
	}
	var out map[string]any
	if err := c.do(ctx, "POST", c.api.Register, body, &out); err != nil {
		return Session{}, err
	}
	s := c.readSession(out)
	if s.Token == "" {
		// some servers sign in only after the first login
		return s, nil
	}
	c.keep(s)
	return s, nil
}

// SendCode asks the server to email a verification code.
func (c *Client) SendCode(ctx context.Context, email string) error {
	if c.api.SendCode == "" {
		return errors.New("this build's account API sends no verification code")
	}
	return c.do(ctx, "POST", c.api.SendCode, map[string]any{"email": email}, nil)
}

// Keys lists the account's API keys.
func (c *Client) Keys(ctx context.Context) ([]Key, error) {
	if c.api.ListKeys == "" {
		return nil, errors.New("this build's account API lists no keys")
	}
	var raw json.RawMessage
	if err := c.do(ctx, "GET", c.api.ListKeys, nil, &raw); err != nil {
		return nil, err
	}
	return decodeKeys(raw), nil
}

// CreateKey makes a key named name, in group when the server has groups.
func (c *Client) CreateKey(ctx context.Context, name, group string) (Key, error) {
	if c.api.CreateKey == "" {
		return Key{}, errors.New("this build's account API creates no keys")
	}
	body := map[string]any{"name": name}
	if group != "" {
		body["group"] = group
	}
	var out map[string]any
	if err := c.do(ctx, "POST", c.api.CreateKey, body, &out); err != nil {
		return Key{}, err
	}
	k := readKey(out)
	if k.Key == "" {
		// a server that answers only an id: list them and take it
		keys, err := c.Keys(ctx)
		if err != nil {
			return Key{}, err
		}
		for _, have := range keys {
			if have.ID == k.ID || (k.ID == "" && have.Name == name) {
				return have, nil
			}
		}
		return Key{}, errors.New("the key was created but its secret could not be read back")
	}
	return k, nil
}

// Groups lists the model groups a key may be made in, empty when the
// server has none.
func (c *Client) Groups(ctx context.Context) ([]Group, error) {
	if c.api.ListGroups == "" {
		return nil, nil
	}
	var raw json.RawMessage
	if err := c.do(ctx, "GET", c.api.ListGroups, nil, &raw); err != nil {
		return nil, err
	}
	var groups []Group
	if firstJSON(raw, &groups) {
		return groups, nil
	}
	return nil, nil
}

// EnsureKey is the one call a first run makes: the account's key if it has
// one, else a new key named after the build.
func (c *Client) EnsureKey(ctx context.Context) (Key, error) {
	keys, err := c.Keys(ctx)
	if err != nil {
		return Key{}, err
	}
	if len(keys) > 0 {
		return keys[0], nil
	}
	return c.CreateKey(ctx, brand.ShortName(), "")
}

// --- wire ---

// do asks the server at path (relative to the config's BaseURL unless
// absolute) and decodes the answer into out. Servers wrap answers two
// ways — the object itself, or {code, message, data} — and both are handed
// out as the object.
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	u, err := c.resolve(path)
	if err != nil {
		return err
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.mu.Lock()
	token := c.sess.Token
	c.mu.Unlock()
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := source.Do(c.http, req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return err
	}
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return fmt.Errorf("sign-in refused (%s): sign in again", res.Status)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s%s", method, path, res.Status, serverMessage(b))
	}
	if out == nil || len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	// an envelope's data is the object; anything else is handed over as it
	// came
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(b, &env) == nil && len(env.Data) > 0 && !bytes.Equal(env.Data, []byte("null")) {
		b = env.Data
	}
	return json.Unmarshal(b, out)
}

// resolve joins path to the config's BaseURL, unless path is a whole URL.
// A path of its own (starting with "/") is the server's root's, whatever
// path the base has; a relative one goes after it.
func (c *Client) resolve(path string) (string, error) {
	if u, err := url.Parse(path); err == nil && u.IsAbs() {
		return path, nil
	}
	if strings.HasPrefix(path, "/") {
		u, err := url.Parse(c.api.BaseURL)
		if err != nil {
			return "", fmt.Errorf("account base URL: %w", err)
		}
		u.Path, u.RawPath, u.RawQuery, u.Fragment = "", "", "", ""
		return strings.TrimSuffix(u.String(), "/") + path, nil
	}
	return strings.TrimSuffix(c.api.BaseURL, "/") + "/" + path, nil
}

// readSession reads the token and user from a sign-in's answer, under the
// field names the config gives (token and email by default).
func (c *Client) readSession(m map[string]any) Session {
	tokenField := or(c.api.Session.Token, "token")
	userField := or(c.api.Session.User, "email")
	s := Session{}
	for _, k := range []string{tokenField, "token", "access_token"} {
		if v, ok := m[k].(string); ok && v != "" {
			s.Token = v
			break
		}
	}
	for _, k := range []string{userField, "email", "username", "name"} {
		if v, ok := m[k].(string); ok && v != "" {
			s.User = v
			break
		}
	}
	return s
}

func readKey(m map[string]any) Key {
	k := Key{}
	for _, f := range []struct {
		dst  *string
		keys []string
	}{
		{&k.ID, []string{"id", "key_id"}},
		{&k.Name, []string{"name", "key_name"}},
		{&k.Key, []string{"key", "api_key", "secret", "token"}},
		{&k.Group, []string{"group", "group_id"}},
	} {
		for _, name := range f.keys {
			switch v := m[name].(type) {
			case string:
				if v != "" {
					*f.dst = v
				}
			case float64:
				*f.dst = fmt.Sprint(int64(v))
			}
			if *f.dst != "" {
				break
			}
		}
	}
	return k
}

// decodeKeys reads a key list as servers send it: a bare array, {keys},
// {items}, or {list}.
func decodeKeys(raw json.RawMessage) []Key {
	var arr []map[string]any
	if json.Unmarshal(raw, &arr) == nil {
		return keysOf(arr)
	}
	var box struct {
		Keys  []map[string]any `json:"keys"`
		Items []map[string]any `json:"items"`
		List  []map[string]any `json:"list"`
	}
	if json.Unmarshal(raw, &box) == nil {
		return keysOf(firstMaps(box.Keys, box.Items, box.List))
	}
	return nil
}

func firstMaps(lists ...[]map[string]any) []map[string]any {
	for _, l := range lists {
		if len(l) > 0 {
			return l
		}
	}
	return nil
}

func keysOf(arr []map[string]any) []Key {
	out := make([]Key, 0, len(arr))
	for _, m := range arr {
		out = append(out, readKey(m))
	}
	return out
}

func firstJSON(raw json.RawMessage, out any) bool {
	return json.Unmarshal(raw, out) == nil
}

func serverMessage(b []byte) string {
	var m struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(b, &m) == nil {
		if m.Message != "" {
			return m.Message
		}
		if m.Error != "" {
			return m.Error
		}
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// --- kept session ---

func sessionFile() string { return filepath.Join(appdir.Config(), "account.json") }

func (c *Client) keep(s Session) {
	c.mu.Lock()
	c.sess = s
	c.mu.Unlock()
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(sessionFile()), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(sessionFile(), b, 0o600)
}

func (c *Client) restore() {
	b, err := os.ReadFile(sessionFile())
	if err != nil {
		return
	}
	var s Session
	if json.Unmarshal(b, &s) == nil {
		c.mu.Lock()
		c.sess = s
		c.mu.Unlock()
	}
}

func or(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}
