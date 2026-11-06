package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/onboard"
)

const onboardUsage = `usage:
  magpie onboard                          the first run's state
  magpie onboard login <email> <password>
  magpie onboard register <email> <password> [code]
  magpie onboard code <email>             email a verification code
  magpie onboard relay                    add the relay with an account key
  magpie onboard connect [<agent>…]       connect agents (every detected one when none named)
  magpie onboard finish                   mark the first run done
  magpie onboard reset                    offer the first run again`

// onboardCmd is a white-label build's first run, from the terminal: sign in
// to the relay, add it, connect agents. The same steps the window's first
// run makes (internal/onboard).
func onboardCmd(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Println(onboardUsage)
		return nil
	}
	r, err := onboard.Ensure()
	if err != nil {
		if errors.Is(err, onboard.ErrNoSignIn) {
			return fmt.Errorf("this build has no account API: name one under account in its brand config, or add a key with magpie provider add")
		}
		return err
	}
	verb := ""
	if len(args) > 0 {
		verb, args = args[0], args[1:]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	switch verb {
	case "", "status":
		return onboardStatus(r)
	case "login":
		if len(args) < 2 {
			return fmt.Errorf("%s", onboardUsage)
		}
		if err := r.SignIn(ctx, args[0], args[1]); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), "signed in as", bold.Render(args[0]))
		return onboardStatus(r)
	case "register":
		if len(args) < 2 {
			return fmt.Errorf("%s", onboardUsage)
		}
		code := ""
		if len(args) > 2 {
			code = args[2]
		}
		if err := r.Register(ctx, args[0], args[1], code); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), "registered", bold.Render(args[0]))
		return onboardStatus(r)
	case "code":
		if len(args) < 1 {
			return fmt.Errorf("%s", onboardUsage)
		}
		if err := r.SendCode(ctx, args[0]); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), "code sent to", bold.Render(args[0]))
		return nil
	case "relay":
		id, err := r.AddRelay(ctx)
		if err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), "relay added as", bold.Render(id))
		return onboardStatus(r)
	case "connect":
		ids := args
		if len(ids) == 0 {
			var found []string
			for _, a := range agent.Detected() {
				if !a.Wired() {
					found = append(found, a.ID)
				}
			}
			ids = found
		}
		if len(ids) == 0 {
			fmt.Println(muted.Render("no agent to connect: none is detected here, or every one is connected"))
			return nil
		}
		failed := r.Connect(ctx, ids)
		for _, id := range ids {
			if msg, bad := failed[id]; bad {
				fmt.Println(amber.Render("!"), bold.Render(id), msg)
			} else {
				fmt.Println(green.Render("✓"), "connected", bold.Render(id))
			}
		}
		if len(failed) > 0 {
			return fmt.Errorf("%d agent(s) failed to connect", len(failed))
		}
		return nil
	case "finish":
		if err := r.Finish(); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), "first run marked done")
		return nil
	case "reset":
		if err := r.Reset(); err != nil {
			return err
		}
		fmt.Println(green.Render("✓"), "first run reset")
		return nil
	}
	return fmt.Errorf("unknown onboard command %q\n\n%s", verb, onboardUsage)
}

func onboardStatus(r *onboard.Runner) error {
	s := r.Status()
	line := func(k, v string) string { return "  " + muted.Render(k) + " " + v }
	signed := muted.Render("no")
	if s.SignedIn {
		signed = green.Render("yes") + muted.Render(" ("+s.User+")")
	}
	fmt.Println(bold.Render(onboard.Name()), muted.Render("first run"))
	fmt.Println(line("signed in:", signed))
	relay := muted.Render("not added")
	if s.Provider != "" {
		relay = green.Render(s.Provider)
	}
	fmt.Println(line("relay:", relay))
	fmt.Println(line("done:", map[bool]string{true: green.Render("yes"), false: muted.Render("no")}[s.Done]))
	if len(s.Detected) == 0 {
		fmt.Println(line("agents:", muted.Render("none detected here")))
		return nil
	}
	var names []string
	for _, a := range s.Detected {
		if a.Connected {
			names = append(names, a.Name+muted.Render(" (connected)"))
		} else {
			names = append(names, a.Name)
		}
	}
	fmt.Println(line("agents:", strings.Join(names, ", ")))
	return nil
}
