package brand

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/source"
)

// A build's icon comes from its config too: downloaded once and kept beside
// the config's cache, then handed to the tray and the window. A build whose
// config names none — or whose icon can't be read — keeps magpie's own.

func iconFile() string { return filepath.Join(appdir.Cache(), "brand-icon.png") }

func iconSourceFile() string { return filepath.Join(appdir.Cache(), "brand-icon.url") }

// IconBytes is the icon the config names, as kept on disk: nil when none is
// named, it hasn't been fetched yet, or what came wasn't a PNG.
func IconBytes() []byte {
	b, err := os.ReadFile(iconFile())
	if err != nil || len(b) == 0 {
		return nil
	}
	// a file that isn't a picture is no icon: the tray reads it every
	// launch
	if _, err := png.DecodeConfig(bytes.NewReader(b)); err != nil {
		return nil
	}
	return b
}

// iconWanted is whether the icon at u should be fetched: none is kept yet,
// or the config now names another.
func iconWanted(u string) bool {
	if u == "" {
		return false
	}
	if IconBytes() == nil {
		return true
	}
	b, err := os.ReadFile(iconSourceFile())
	return err != nil || strings.TrimSpace(string(b)) != u
}

// fetchIcon downloads the icon at u to iconFile, through the proxy the rest
// of magpie's requests take.
func fetchIcon(ctx context.Context, u string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	res, err := source.Do(client, req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("icon %s: %s", u, res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("icon %s: %w", u, err)
	}
	if _, err := png.DecodeConfig(bytes.NewReader(b)); err != nil {
		return errors.New("icon " + u + ": not a PNG")
	}
	if err := os.MkdirAll(filepath.Dir(iconFile()), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(iconFile(), b, 0o644); err != nil {
		return err
	}
	return os.WriteFile(iconSourceFile(), []byte(u), 0o644)
}
