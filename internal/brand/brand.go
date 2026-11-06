// Package brand carries the white-label configuration of a build: the name,
// icon, site and endpoints it shows. Nothing about a particular brand is
// compiled in. A build knows only where its config lives — ConfigURL, set at
// link time (-ldflags "-X .../internal/brand.ConfigURL=...") — and reads the
// rest from that file: the brand's name and icon, the update feed, the
// partner list (a relay's presets), and the account API a sign-in calls.
//
// Until the file is read, or when it can't be reached, the config is the
// upstream one (see Default), so a build without a ConfigURL behaves exactly
// as magpie always has.
package brand

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/source"
)

// ConfigURL is where the build's config lives. A release sets it at link
// time:
//
//	go build -ldflags "-X github.com/yetone/magpie/internal/brand.ConfigURL=https://example.com/api/desktop/config.json"
//
// MAGPIE_BRAND_CONFIG moves it at run time, which is how a build is tried
// against a local file or server; MAGPIE_BRAND_CONFIG=off keeps to Default.
var ConfigURL = ""

// Config is one build's white-label configuration.
type Config struct {
	Schema int   `json:"schema"`
	Brand  Brand `json:"brand"`
	// Updates is where this build's releases are described and how their
	// files are named (see update.Feed and update.AppAsset).
	Updates Updates `json:"updates"`
	// Partners is the relay/partner list's address, where a build's own
	// presets are published (see provider.PartnersURL).
	Partners string `json:"partners,omitempty"`
	// Account, when set, is the sign-in API the desktop assistant offers.
	// Every path is configuration: a server's routes are never assumed.
	Account *Account `json:"account,omitempty"`
	// Provider, when set, is the relay the assistant adds by default.
	Provider *Provider `json:"provider,omitempty"`
	// Onboarding, when set, is where the first-run pages point.
	Onboarding *Onboarding `json:"onboarding,omitempty"`
}

// Brand is the name and site a build shows.
type Brand struct {
	// Name is the product's name where a window or menu says it
	// ("Acme Desktop").
	Name string `json:"name,omitempty"`
	// ShortName is the name its copy uses mid-sentence ("Acme").
	ShortName string `json:"shortName,omitempty"`
	// Site is the product's home, opened for help and release notes.
	Site string `json:"site,omitempty"`
	// Icon is a PNG fetched once and kept, shown in the tray and window,
	// or "" for the build's own.
	Icon string `json:"icon,omitempty"`
}

// Updates is where a build's releases are.
type Updates struct {
	// Feed describes the newest release, in the format update.Latest
	// reads.
	Feed string `json:"feed,omitempty"`
	// AssetPrefix names release files: "<prefix>-darwin-arm64.zip",
	// "<prefix>-windows-amd64.zip", "<prefix>-cli-linux-arm64". Defaults
	// to "magpie".
	AssetPrefix string `json:"assetPrefix,omitempty"`
}

// Account is a relay's sign-in API. Paths are relative to BaseURL unless
// they are absolute; a path left out is a route the server doesn't have.
type Account struct {
	BaseURL string `json:"baseURL"`
	// Login posts {email, password} and answers a session (see the
	// session field names below).
	Login string `json:"login,omitempty"`
	// Register creates an account with the same body as Login.
	Register string `json:"register,omitempty"`
	// SendCode asks for an email verification code.
	SendCode string `json:"sendCode,omitempty"`
	// ListKeys answers the account's API keys.
	ListKeys string `json:"listKeys,omitempty"`
	// CreateKey makes one, named by the client.
	CreateKey string `json:"createKey,omitempty"`
	// ListGroups answers the model groups a key may be made in.
	ListGroups string `json:"listGroups,omitempty"`
	// OAuthStart and OAuthPoll drive a browser sign-in, when the server
	// has one: Start answers {url, state}, Poll is asked with the state
	// until it answers a session.
	OAuthStart string `json:"oauthStart,omitempty"`
	OAuthPoll  string `json:"oauthPoll,omitempty"`
	// Session is what a successful sign-in answers: the field holding
	// the token and, when the server announces it, the field holding the
	// user's name. Defaults are "token" and "email".
	Session Session `json:"session,omitempty"`
}

// Session names the fields a sign-in's answer carries.
type Session struct {
	Token string `json:"token,omitempty"` // default "token"
	User  string `json:"user,omitempty"`  // default "email"
}

// Provider is the relay the assistant offers out of the box: adding it asks
// only for a key.
type Provider struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind,omitempty"` // "relay" unless set
	Chat      string `json:"chat,omitempty"`
	Responses string `json:"responses,omitempty"`
	Anthropic string `json:"anthropic,omitempty"`
	KeysURL   string `json:"keysUrl,omitempty"`
	Website   string `json:"website,omitempty"`
	// Lines are the relay's regional endpoints, tried in order by the
	// local router (see the gateway's line health).
	Lines []Line `json:"lines,omitempty"`
}

// Line is one endpoint of a relay that serves several.
type Line struct {
	Name      string `json:"name"`
	Chat      string `json:"chat"`
	Responses string `json:"responses,omitempty"`
	Anthropic string `json:"anthropic,omitempty"`
}

// Onboarding is where a first run sends the user.
type Onboarding struct {
	// WelcomeURL is the product's own page about the desktop app.
	WelcomeURL string `json:"welcomeURL,omitempty"`
	// RegisterURL is the sign-up page, for a server without an API for
	// it.
	RegisterURL string `json:"registerURL,omitempty"`
}

// Default is the upstream build's configuration: what a build with no
// ConfigURL, or one whose config can't be read, shows.
func Default() Config {
	return Config{
		Schema: 1,
		Brand: Brand{
			Name:      "magpie",
			ShortName: "magpie",
			Site:      "https://usemagpie.ai",
		},
		Updates: Updates{
			Feed:        "https://usemagpie.ai/api/latest",
			AssetPrefix: "magpie",
		},
		Partners: "https://usemagpie.ai/api/partners",
	}
}

var (
	mu      sync.RWMutex
	current = Default()
	// loaded is closed by the first Load, once a cache or a fetch has had
	// its say; Wait reads it.
	loaded   = make(chan struct{})
	loadOnce sync.Once
)

// Get is the configuration in force: the fetched one, else the kept one,
// else Default. It never blocks on the network.
func Get() Config {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// Name is the product's name for a window or a menu.
func Name() string { return or(Get().Brand.Name, Default().Brand.Name) }

// ShortName is the name its copy uses mid-sentence.
func ShortName() string { return or(Get().Brand.ShortName, Name()) }

// Site is the product's home.
func Site() string { return or(Get().Brand.Site, Default().Brand.Site) }

// Icon is the icon's address, "" when the build's own is shown.
func Icon() string { return Get().Brand.Icon }

// Feed is the update feed's address, "" when Default's is used.
func Feed() string { return Get().Updates.Feed }

// AssetPrefix names release files, "magpie" unless configured.
func AssetPrefix() string { return or(Get().Updates.AssetPrefix, Default().Updates.AssetPrefix) }

// PartnersURL is the partner list's address, "" when Default's is used.
func PartnersURL() string { return Get().Partners }

// SignIn is the sign-in API, nil when the build has none.
func SignIn() *Account { return Get().Account }

// Relay is the relay offered out of the box, nil when there is none.
func Relay() *Provider { return Get().Provider }

// Guide is where a first run sends the user, nil when unset.
func Guide() *Onboarding { return Get().Onboarding }

func or(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

// configURL is where this run reads the config from: MAGPIE_BRAND_CONFIG
// when set (off means none), else the linked-in ConfigURL.
func configURL() string {
	if u := os.Getenv("MAGPIE_BRAND_CONFIG"); u != "" {
		return u
	}
	return ConfigURL
}

func cacheFile() string { return filepath.Join(appdir.Cache(), "brand.json") }

// Load reads the kept config and then asks for a newer one, behind the call
// when wait is 0. Safe to call more than once: the first call does the work.
// Wait tells when it finished.
func Load(ctx context.Context, wait time.Duration) {
	loadOnce.Do(func() {
		readCache()
		u := configURL()
		if u == "" || u == "off" {
			close(loaded)
			return
		}
		if testing.Testing() && os.Getenv("MAGPIE_BRAND_CONFIG") == "" {
			// a test never asks a real config; one that wants it names
			// its own with MAGPIE_BRAND_CONFIG
			close(loaded)
			return
		}
		if wait <= 0 {
			go fetchAndClose(ctx, u)
			return
		}
		fetchAndClose(ctx, u)
	})
}

func fetchAndClose(ctx context.Context, u string) {
	defer close(loaded)
	if err := fetchOnce(ctx, u); err != nil {
		fmt.Fprintf(os.Stderr, "brand: %v\n", err)
	}
}

// Wait waits until the first Load has finished, at most timeout: a command
// that must read the fetched config before it acts (update) waits here.
func Wait(ctx context.Context, timeout time.Duration) {
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-loaded:
	case <-t.C:
	case <-ctx.Done():
	}
}

// KeepFresh fetches the config when it is due: every hour, and ten minutes
// after a failed fetch, so a change on the server reaches a running app.
func KeepFresh(ctx context.Context) {
	u := configURL()
	if u == "" || u == "off" || (testing.Testing() && os.Getenv("MAGPIE_BRAND_CONFIG") == "") {
		return
	}
	t := time.NewTimer(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		err := fetchOnce(ctx, u)
		if err != nil {
			t.Reset(10 * time.Minute)
		} else {
			t.Reset(time.Hour)
		}
	}
}

func readCache() {
	b, err := os.ReadFile(cacheFile())
	if err != nil {
		return
	}
	var c Config
	if json.Unmarshal(b, &c) != nil {
		return
	}
	set(c)
}

func set(c Config) {
	mu.Lock()
	current = merge(c)
	mu.Unlock()
}

// merge fills a fetched config's empty fields from Default, so a config that
// names only its brand still updates and lists partners as upstream does.
func merge(c Config) Config {
	d := Default()
	if strings.TrimSpace(c.Brand.Name) == "" {
		c.Brand.Name = d.Brand.Name
	}
	if strings.TrimSpace(c.Brand.ShortName) == "" {
		c.Brand.ShortName = c.Brand.Name
	}
	if strings.TrimSpace(c.Brand.Site) == "" {
		c.Brand.Site = d.Brand.Site
	}
	if strings.TrimSpace(c.Updates.Feed) == "" {
		c.Updates.Feed = d.Updates.Feed
	}
	if strings.TrimSpace(c.Updates.AssetPrefix) == "" {
		c.Updates.AssetPrefix = d.Updates.AssetPrefix
	}
	if strings.TrimSpace(c.Partners) == "" {
		c.Partners = d.Partners
	}
	return c
}

func fetchOnce(ctx context.Context, u string) error {
	var b []byte
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			return err
		}
		client := &http.Client{Timeout: 30 * time.Second}
		res, err := source.Do(client, req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return fmt.Errorf("config %s: %s", u, res.Status)
		}
		b, err = io.ReadAll(io.LimitReader(res.Body, 4<<20))
		if err != nil {
			return fmt.Errorf("config %s: %w", u, err)
		}
	} else {
		// a file, for MAGPIE_BRAND_CONFIG pointing at one while a build
		// is tried out
		var err error
		b, err = os.ReadFile(strings.TrimPrefix(u, "file://"))
		if err != nil {
			return err
		}
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return fmt.Errorf("config %s: %w", u, err)
	}
	c = merge(c)
	set(c)
	if b, err := json.MarshalIndent(c, "", "  "); err == nil {
		if err := os.MkdirAll(filepath.Dir(cacheFile()), 0o755); err == nil {
			_ = os.WriteFile(cacheFile(), b, 0o644)
		}
	}
	return nil
}
