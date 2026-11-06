// Package onboard is a white-label build's first run: sign in to the relay
// the build belongs to, add it as a provider with one of the account's keys,
// and connect the agents already on this machine. A build with no sign-in
// API in its brand config has no first run to make (Open is nil) and keeps
// magpie's own flow.
package onboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/account"
	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/brand"
	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/source"
)

// Status is what a first run shows and what it has done so far.
type Status struct {
	// HasSignIn is whether this build has an account API at all.
	HasSignIn bool   `json:"hasSignIn"`
	SignedIn  bool   `json:"signedIn"`
	User      string `json:"user,omitempty"`
	// Provider is the relay's provider id once added, "" before.
	Provider string `json:"provider,omitempty"`
	// Group is the routing group over the relay's lines once made, "" when
	// the config names no lines.
	Group string `json:"group,omitempty"`
	// Done is set once the first run finished; the window stops showing it.
	Done bool `json:"done"`
	// Detected are the agents found on this machine, with whether each is
	// connected to magpie.
	Detected []Agent `json:"detected"`
}

// Agent is one agent a first run offers to connect.
type Agent struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
}

// Runner is the first run of this build.
type Runner struct {
	c    *account.Client
	kept keptState
}

// keptState is the part of a run kept on disk between launches.
type keptState struct {
	Provider string `json:"provider,omitempty"`
	// Group is the routing group over the relay's lines, "" when the
	// config names none.
	Group string `json:"group,omitempty"`
	Done  bool   `json:"done,omitempty"`
}

func stateFile() string { return filepath.Join(appdir.Config(), "onboard.json") }

// HasSignIn is whether this build has an account API at all: a page asks
// before it offers a first run.
func HasSignIn() bool { return account.Enabled() }

// Open is this build's first run, or nil when it has no sign-in API.
func Open() *Runner {
	c := account.Open()
	if c == nil {
		return nil
	}
	r := &Runner{c: c}
	if b, err := os.ReadFile(stateFile()); err == nil {
		_ = json.Unmarshal(b, &r.kept)
	}
	return r
}

// Status is the run's state now: signed in or not, the relay added or not,
// and the agents on this machine.
func (r *Runner) Status() Status {
	s := Status{
		HasSignIn: true,
		SignedIn:  r.c.SignedIn(),
		User:      r.c.Session().User,
		Provider:  r.kept.Provider,
		Group:     r.kept.Group,
		Done:      r.kept.Done,
	}
	for _, a := range agent.Detected() {
		s.Detected = append(s.Detected, Agent{ID: a.ID, Name: a.Name, Connected: a.Wired()})
	}
	return s
}

// SignIn signs in to the relay.
func (r *Runner) SignIn(ctx context.Context, email, password string) error {
	_, err := r.c.Login(ctx, email, password)
	return err
}

// Register creates an account on the relay.
func (r *Runner) Register(ctx context.Context, email, password, code string) error {
	_, err := r.c.Register(ctx, email, password, code)
	return err
}

// SendCode asks the relay to email a verification code.
func (r *Runner) SendCode(ctx context.Context, email string) error {
	return r.c.SendCode(ctx, email)
}

// SignOut forgets the session (the provider and its key stay).
func (r *Runner) SignOut() error {
	r.c.SignOut()
	return nil
}

// AddRelay adds the relay as a provider with one of the account's keys —
// the first, or a new one — and asks it for its models. It is done once;
// the provider's id is kept.
func (r *Runner) AddRelay(ctx context.Context) (string, error) {
	if r.kept.Provider != "" {
		return r.kept.Provider, nil
	}
	if !r.c.SignedIn() {
		return "", errors.New("sign in first")
	}
	b := brand.Relay()
	if b == nil {
		return "", errors.New("this build's config names no relay")
	}
	key, err := r.c.EnsureKey(ctx)
	if err != nil {
		return "", err
	}
	p := provider.Provider{
		Name:      b.Name,
		Chat:      b.Chat,
		Responses: b.Responses,
		Anthropic: b.Anthropic,
		KeysURL:   b.KeysURL,
		Website:   b.Website,
		Key:       key.Key,
	}
	id, err := provider.Add(p)
	if err != nil {
		return "", err
	}
	// the models, so an agent has something to pick; a relay that doesn't
	// answer the list is no reason to fail the run
	var models []catalog.Model
	if saved, err := provider.Find(id); err == nil {
		fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		models, _ = saved.Fetch(fctx)
		cancel()
	}
	r.kept.Provider = id
	// the lines the config names: one provider each, and a routing group
	// over them that the gateway fails over between
	if len(b.Lines) > 0 && len(models) > 0 {
		if gid := r.addLines(context.WithoutCancel(ctx), id, b, models, key.Key); gid != "" {
			r.kept.Group = gid
		}
	}
	r.save()
	return id, nil
}

// addLines adds one provider per line the relay's config names and a
// routing group whose members are each model on each line, the lines' own
// order kept: a line that times out or fails is passed over for the next
// on the same model (the gateway's fallback), which is what "线路出问题，
// 自动换一条" means. The main provider's line comes first. "" when fewer
// than two lines were added, a group of one being no failover.
func (r *Runner) addLines(ctx context.Context, main string, b *brand.Provider, models []catalog.Model, key string) string {
	ids := []string{main}
	for _, ln := range b.Lines {
		lp := provider.Provider{
			Name:      b.Name + " · " + ln.Name,
			Chat:      ln.Chat,
			Responses: ln.Responses,
			Anthropic: ln.Anthropic,
			KeysURL:   b.KeysURL,
			Website:   b.Website,
			Key:       key,
		}
		lid, err := provider.Add(lp)
		if err != nil {
			continue
		}
		if saved, err := provider.Find(lid); err == nil {
			fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			_, _ = saved.Fetch(fctx)
			cancel()
		}
		ids = append(ids, lid)
	}
	if len(ids) < 2 {
		return ""
	}
	// each model on each line, in the line order: the same model is tried
	// on the next line before the next model
	var members []string
	for _, m := range models {
		for _, lid := range ids {
			members = append(members, lid+"/"+m.ID)
		}
	}
	g := provider.Group{
		ID:      main + "-lines",
		Name:    b.Name + " · 线路",
		Members: members,
	}
	if err := provider.SaveGroup(g); err != nil {
		// the relay is added either way; the lines' group is the part
		// that didn't take, and its reason belongs in the log
		fmt.Fprintf(os.Stderr, "onboard: lines group: %v\n", err)
		return ""
	}
	return g.ID
}

// Connect connects the named agents to magpie, each with its own error.
// Agents already connected are left as they are.
func (r *Runner) Connect(ctx context.Context, ids []string) map[string]string {
	out := map[string]string{}
	all := agent.All()
	for _, id := range ids {
		var found *agent.Agent
		for _, a := range all {
			if a.ID == id {
				found = a
				break
			}
		}
		if found == nil {
			out[id] = "no such agent"
			continue
		}
		if found.Wired() {
			continue
		}
		if err := found.Connect(); err != nil {
			out[id] = err.Error()
		}
	}
	return out
}

// Finish marks the first run done, so it isn't shown again.
func (r *Runner) Finish() error {
	r.kept.Done = true
	return r.save()
}

// LineStatus is one of the relay's lines as it answers now: asked only
// when the caller wants a probe (Probe), so a page may list them without
// a request each.
type LineStatus struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	// Probed is whether the line was asked; Latency and OK mean nothing
	// before it was.
	Probed  bool `json:"probed"`
	OK      bool `json:"ok"`
	Latency int  `json:"latencyMs,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Lines are the relay's lines, the main one first, asked in parallel when
// probe is set: each line's models endpoint, to time it. A line answering
// any status has arrived; a key's refusal (401) is still a line that
// answers, so the timing is the line's.
func (r *Runner) Lines(ctx context.Context, probe bool) []LineStatus {
	b := brand.Relay()
	if b == nil {
		return nil
	}
	out := []LineStatus{{Name: "main", URL: b.Chat}}
	for _, ln := range b.Lines {
		out = append(out, LineStatus{Name: ln.Name, URL: ln.Chat})
	}
	if !probe {
		return out
	}
	var wg sync.WaitGroup
	for i := range out {
		wg.Add(1)
		go func(s *LineStatus) {
			defer wg.Done()
			s.probe(ctx)
		}(&out[i])
	}
	wg.Wait()
	return out
}

// probe asks the line's models endpoint and records how it answered.
func (s *LineStatus) probe(ctx context.Context) {
	s.Probed = true
	u := strings.TrimSuffix(s.URL, "/") + "/models"
	tctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(tctx, "GET", u, nil)
	if err != nil {
		s.Error = err.Error()
		return
	}
	start := time.Now()
	res, err := source.Do(&http.Client{}, req)
	if err != nil {
		s.Error = err.Error()
		return
	}
	defer res.Body.Close()
	s.Latency = int(time.Since(start).Milliseconds())
	s.OK = res.StatusCode < 500
	if !s.OK {
		s.Error = res.Status
	}
}

// Reset forgets what the run did, so it is offered again (a test's and a
// support tool's).
func (r *Runner) Reset() error {
	r.kept = keptState{}
	return r.save()
}

func (r *Runner) save() error {
	if err := os.MkdirAll(filepath.Dir(stateFile()), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(r.kept)
	if err != nil {
		return err
	}
	return os.WriteFile(stateFile(), b, 0o600)
}

// Name is the relay's name, for the pages a first run shows.
func Name() string {
	if b := brand.Relay(); b != nil && strings.TrimSpace(b.Name) != "" {
		return b.Name
	}
	return brand.ShortName()
}

// ErrNoSignIn is Open's absence said plainly.
var ErrNoSignIn = errors.New("this build has no account API")

// Ensure is Open for a caller that can't take nil.
func Ensure() (*Runner, error) {
	if r := Open(); r != nil {
		return r, nil
	}
	return nil, ErrNoSignIn
}
