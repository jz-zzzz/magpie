package gui

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/yetone/magpie/internal/onboard"
)

// onboardRoutes serves a white-label build's first run: sign in to the relay
// its config names, add it with one of the account's keys, and connect the
// agents already on this machine. A build with no account API in its config
// answers hasSignIn: false, and the page keeps magpie's own flow (a key
// pasted by hand).
func onboardRoutes(mux *http.ServeMux) {
	if !onboard.HasSignIn() {
		mux.HandleFunc("GET /api/onboard", func(rw http.ResponseWriter, r *http.Request) {
			writeJSON(rw, map[string]any{"hasSignIn": false})
		})
		return
	}

	runner := func(rw http.ResponseWriter) *onboard.Runner {
		r := onboard.Open()
		if r == nil {
			http.NotFound(rw, nil)
		}
		return r
	}

	mux.HandleFunc("GET /api/onboard", func(rw http.ResponseWriter, r *http.Request) {
		run := runner(rw)
		if run == nil {
			return
		}
		writeJSON(rw, run.Status())
	})

	mux.HandleFunc("POST /api/onboard/signin", func(rw http.ResponseWriter, r *http.Request) {
		run := runner(rw)
		if run == nil {
			return
		}
		var in struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if !decodeBody(rw, r, &in) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := run.SignIn(ctx, in.Email, in.Password); err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, run.Status())
	})

	mux.HandleFunc("POST /api/onboard/register", func(rw http.ResponseWriter, r *http.Request) {
		run := runner(rw)
		if run == nil {
			return
		}
		var in struct {
			Email    string `json:"email"`
			Password string `json:"password"`
			Code     string `json:"code"`
		}
		if !decodeBody(rw, r, &in) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := run.Register(ctx, in.Email, in.Password, in.Code); err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, run.Status())
	})

	mux.HandleFunc("POST /api/onboard/code", func(rw http.ResponseWriter, r *http.Request) {
		run := runner(rw)
		if run == nil {
			return
		}
		var in struct {
			Email string `json:"email"`
		}
		if !decodeBody(rw, r, &in) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := run.SendCode(ctx, in.Email); err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, map[string]any{"sent": true})
	})

	mux.HandleFunc("POST /api/onboard/relay", func(rw http.ResponseWriter, r *http.Request) {
		run := runner(rw)
		if run == nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		id, err := run.AddRelay(ctx)
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, map[string]any{"provider": id})
	})

	mux.HandleFunc("POST /api/onboard/connect", func(rw http.ResponseWriter, r *http.Request) {
		run := runner(rw)
		if run == nil {
			return
		}
		var in struct {
			IDs []string `json:"ids"`
		}
		if !decodeBody(rw, r, &in) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		errs := run.Connect(ctx, in.IDs)
		writeJSON(rw, map[string]any{"errors": errs, "status": run.Status()})
	})

	mux.HandleFunc("POST /api/onboard/finish", func(rw http.ResponseWriter, r *http.Request) {
		run := runner(rw)
		if run == nil {
			return
		}
		if err := run.Finish(); err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, run.Status())
	})

	mux.HandleFunc("POST /api/onboard/reset", func(rw http.ResponseWriter, r *http.Request) {
		run := runner(rw)
		if run == nil {
			return
		}
		if err := run.Reset(); err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, run.Status())
	})
}

// decodeBody decodes the request's JSON into v, answering a 400 when it
// isn't JSON the caller meant.
func decodeBody(rw http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 1<<20)).Decode(v); err != nil {
		fail(rw, err)
		return false
	}
	return true
}
