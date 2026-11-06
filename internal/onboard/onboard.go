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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/account"
	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/brand"
	"github.com/yetone/magpie/internal/provider"
)

// Status is what a first run shows and what it has done so far.
type Status struct {
	// HasSignIn is whether this build has an account API at all.
	HasSignIn bool   `json:"hasSignIn"`
	SignedIn  bool   `json:"signedIn"`
	User      string `json:"user,omitempty"`
	// Provider is the relay's provider id once added, "" before.
	Provider string `json:"provider,omitempty"`
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
	Done     bool   `json:"done,omitempty"`
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
	if saved, err := provider.Find(id); err == nil {
		fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		_, _ = saved.Fetch(fctx)
		cancel()
	}
	r.kept.Provider = id
	r.save()
	return id, nil
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
