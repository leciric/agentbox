package api

import (
	"context"
	"net/http"
)

// The doctor: `agentbox doctor` and Setup's list of what's wrong with this
// installation. Each check says ok, warn or fail, in a sentence, and what
// fixes it; the ones that are safe to retry the daemon fixes itself when
// asked (POST /v1/doctor/fix). What it can't fix, the Home chat may: POST
// /v1/doctor/home opens a session there, told what failed.

// A DoctorCheck's Status.
const (
	DoctorOK   = "ok"
	DoctorWarn = "warn"
	DoctorFail = "fail"
)

// DoctorAILogin is the check of the Home chat's AI tool's login: the one a
// failure of can't be handed to the Home chat, which couldn't run.
const DoctorAILogin = "ai-login"

type DoctorCheck struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // ok, warn or fail
	// Message says what was found, in a sentence.
	Message string `json:"message"`
	// Fix is what fixes it: a command, or what to do. Empty when ok.
	Fix string `json:"fix,omitempty"`
	// AutoFix says `agentbox doctor --fix` (or the app's Fix) fixes it by
	// itself: it is safe to try again, like a download.
	AutoFix bool `json:"autoFix,omitempty"`
	// Job is the background job fixing it, while one runs.
	Job string `json:"job,omitempty"`
}

type DoctorReport struct {
	// Version is the daemon's.
	Version string        `json:"version"`
	Checks  []DoctorCheck `json:"checks"`
}

// DoctorFixRequest names the checks to fix; none fixes every one that can be.
type DoctorFixRequest struct {
	Checks []string `json:"checks,omitempty"`
}

// DoctorFix is what trying to fix one check did: the job it started, or why
// it couldn't.
type DoctorFix struct {
	ID    string `json:"id"`
	Job   string `json:"job,omitempty"`
	Error string `json:"error,omitempty"`
}

type DoctorFixResult struct {
	Fixes  []DoctorFix  `json:"fixes"`
	Report DoctorReport `json:"report"`
}

// DoctorHomeRequest names the failing check to hand to the Home chat.
type DoctorHomeRequest struct {
	Check string `json:"check"`
}

// DoctorHomeResult is the Home chat session opened for it.
type DoctorHomeResult struct {
	Session string `json:"session"`
}

func (c *Client) Doctor(ctx context.Context) (DoctorReport, error) {
	var r DoctorReport
	return r, c.do(ctx, http.MethodGet, "/v1/doctor", nil, &r)
}

func (c *Client) DoctorFix(ctx context.Context, req DoctorFixRequest) (DoctorFixResult, error) {
	var r DoctorFixResult
	return r, c.do(ctx, http.MethodPost, "/v1/doctor/fix", req, &r)
}

func (c *Client) DoctorHome(ctx context.Context, req DoctorHomeRequest) (DoctorHomeResult, error) {
	var r DoctorHomeResult
	return r, c.do(ctx, http.MethodPost, "/v1/doctor/home", req, &r)
}
