// Package nimbo is the Go port of the Kotlin backend (backend/). The HTTP API and its JSON stay exactly as the
// React UI expects them; this file holds what every area shares so areas can be ported in parallel.
package nimbo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

// ---- Errors -------------------------------------------------------------------------------------------------------
// Each one maps to an HTTP status in StatusFor, exactly like the StatusPages block of App.kt.

type InvalidParam struct{ Msg string }        // 400
type InvalidName struct{ Msg string }         // 400
type UnknownTarget struct{ Msg string }       // 404
type KubeError struct{ Msg string }           // 422
type EmulatorUnreachable struct{ Msg string } // 503

// EmulatorError is 404 when Status is 404 and 502 otherwise.
type EmulatorError struct {
	Status int
	Msg    string
}

// UpdateError is a failure the person should read (a bad checksum, a refused file). Routes choose its status.
type UpdateError struct{ Msg string }

// BadBody is a request body that is missing or not valid JSON for the call (400).
type BadBody struct{}

func (e InvalidParam) Error() string        { return e.Msg }
func (e InvalidName) Error() string         { return e.Msg }
func (e UnknownTarget) Error() string       { return e.Msg }
func (e KubeError) Error() string           { return e.Msg }
func (e EmulatorUnreachable) Error() string { return e.Msg }
func (e EmulatorError) Error() string       { return e.Msg }
func (e UpdateError) Error() string         { return e.Msg }
func (BadBody) Error() string               { return "The request body is missing or not valid JSON for this call" }

// StatusFor returns the HTTP status and the message to send for an error. Unknown errors are a plain 500 that
// does not leak details.
func StatusFor(err error) (int, string) {
	var (
		ip InvalidParam
		in InvalidName
		ut UnknownTarget
		ke KubeError
		eu EmulatorUnreachable
		ee EmulatorError
		bb BadBody
	)
	switch {
	case errors.As(err, &ke):
		return http.StatusUnprocessableEntity, orDefault(ke.Msg, "kubectl failed")
	case errors.As(err, &ip):
		return http.StatusBadRequest, orDefault(ip.Msg, "Invalid parameter")
	case errors.As(err, &in):
		return http.StatusBadRequest, orDefault(in.Msg, "Invalid name")
	case errors.As(err, &ut):
		return http.StatusNotFound, orDefault(ut.Msg, "Unknown target")
	case errors.As(err, &eu):
		return http.StatusServiceUnavailable, orDefault(eu.Msg, "Emulator not reachable")
	case errors.As(err, &ee):
		if ee.Status == 404 {
			return http.StatusNotFound, orDefault(ee.Msg, "Emulator error")
		}
		return http.StatusBadGateway, orDefault(ee.Msg, "Emulator error")
	case errors.As(err, &bb):
		return http.StatusBadRequest, bb.Error()
	}
	return http.StatusInternalServerError, "Internal error"
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// ---- HTTP helpers -------------------------------------------------------------------------------------------------

// HandlerFunc is a handler that returns its failure; Handle turns it into a response using StatusFor.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

type ErrorDto struct {
	Error string `json:"error"`
}

func Handle(f HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := f(w, r); err != nil {
			status, msg := StatusFor(err)
			WriteJSON(w, status, ErrorDto{msg})
		}
	}
}

// WriteJSON sends v as JSON. Nullable fields are sent as null (never omitted) so the UI can rely on the keys.
// A nil v sends only the status (use it for 204).
func WriteJSON(w http.ResponseWriter, status int, v any) {
	if v == nil {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Plain text, not HTML-safe: a placeholder like <NAME> must stay readable in the answer.
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// ReadJSON decodes the body into v, answering BadBody when it is missing or malformed.
func ReadJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(v); err != nil {
		return BadBody{}
	}
	return nil
}

// Param is the value of a {name} path segment; Query is a query-string value.
func Param(r *http.Request, name string) string { return r.PathValue(name) }
func Query(r *http.Request, name string) string { return r.URL.Query().Get(name) }

// QueryEscape and PathEscape exist so areas do not each import net/url just for them.
func QueryEscape(s string) string { return url.QueryEscape(s) }
func PathEscape(s string) string  { return url.PathEscape(s) }

// ---- Running things -----------------------------------------------------------------------------------------------

// ExecResult is what a finished command left: its exit code and combined output (trimmed).
type ExecResult struct {
	ExitCode int
	Output   string
}

func (r ExecResult) OK() bool { return r.ExitCode == 0 }

// Exec runs the docker CLI. Fake it in tests. Arguments are a list: nothing is ever passed through a shell.
type Exec interface {
	Run(ctx context.Context, args []string, timeoutSeconds int) ExecResult
}

// Shell is like Exec with optional standard input (kubectl apply -f -, PowerShell for DPAPI).
type Shell interface {
	Run(ctx context.Context, args []string, stdin *string, timeoutSeconds int) ExecResult
}

// ProcessShell runs Bin with the arguments. Exit 124 means it timed out and 127 that it could not be started.
type ProcessShell struct{ Bin string }

func (p ProcessShell) Run(ctx context.Context, args []string, stdin *string, timeoutSeconds int) ExecResult {
	bin := p.Bin
	if bin == "" {
		bin = "docker"
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, args...)
	hideWindow(cmd)
	if stdin != nil {
		cmd.Stdin = strings.NewReader(*stdin)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if cctx.Err() == context.DeadlineExceeded {
		return ExecResult{124, fmt.Sprintf("timed out after %ds", timeoutSeconds)}
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ExecResult{ee.ExitCode(), strings.TrimSpace(out.String())}
		}
		return ExecResult{127, err.Error()}
	}
	return ExecResult{0, strings.TrimSpace(out.String())}
}

// ProcessExec adapts ProcessShell to Exec.
type ProcessExec struct{ Bin string }

func (p ProcessExec) Run(ctx context.Context, args []string, timeoutSeconds int) ExecResult {
	return ProcessShell{Bin: p.Bin}.Run(ctx, args, nil, timeoutSeconds)
}

// HTTPReply is a status and body from an emulator.
type HTTPReply struct {
	Status int
	Body   string
}

// HTTPGateway talks to the emulators. Fake it in tests. A connection failure is an EmulatorUnreachable.
type HTTPGateway interface {
	Send(ctx context.Context, method, url string, headers map[string]string, body *string) (HTTPReply, error)
}

// DirectHTTP is the real gateway: HTTP/1.1 only, so results match what curl and most SDKs see.
type DirectHTTP struct{}

var emulatorClient = &http.Client{
	Transport: &http.Transport{ForceAttemptHTTP2: false, DisableKeepAlives: true},
	Timeout:   10 * time.Second,
}

func (DirectHTTP) Send(ctx context.Context, method, rawURL string, headers map[string]string, body *string) (HTTPReply, error) {
	var rd io.Reader
	if body != nil {
		rd = strings.NewReader(*body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return HTTPReply{}, InvalidParam{err.Error()}
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := emulatorClient.Do(req)
	if err != nil {
		return HTTPReply{}, EmulatorUnreachable{"Emulator not reachable at " + req.URL.Host}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return HTTPReply{resp.StatusCode, string(b)}, nil
}
