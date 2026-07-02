// Copyright (c) 2026 Michael D Henderson. All rights reserved.

// Package main implements earl, a command line tool that foolishly attempts
// to replace `curl`. Its only redeeming quality is that it's written to smoke
// test our game-server, so it knows how to authenticate: it reads an admin
// session's tokens from a JSON file and attaches the bearer token to every
// request automatically. When the access token is rejected (401) it refreshes
// it, or logs in fresh with EARL_AUTHN_EMAIL/EARL_AUTHN_SECRET, rewrites the
// authn file, and retries the request once.
//
// Example:
//
//	go run ./cmd/earl get /me
//
// issues GET ${EARL_BASE_URL}/me with a valid Authorization: Bearer header.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/peterbourgon/ff/v4"
	"github.com/peterbourgon/ff/v4/ffhelp"

	"github.com/mdhender/ecv4/internal/dotenv"
)

func main() {
	// Load .env files before parsing flags so ff reads EARL_* variables sourced
	// from them. EARL_ENV selects which files load (see internal/dotenv) and is
	// read straight from the environment — not a flag — because it must be known
	// before any flag is parsed. It defaults to development.
	env := os.Getenv("EARL_ENV")
	if env == "" {
		env = "development"
	}
	if err := dotenv.Load(env); err != nil {
		fmt.Fprintf(os.Stderr, "error: load %q environment: %v\n", env, err)
		os.Exit(1)
	}

	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		os.Exit(1)
	}
}

// authn mirrors the AuthTokens response persisted to disk (see
// data/alpha/authn.json). The access token authenticates a request; the
// refresh token lets earl mint a new access token when the old one expires.
type authn struct {
	AccessToken      string `json:"accessToken"`
	ExpiresInSeconds int    `json:"expiresInSeconds"`
	RefreshToken     string `json:"refreshToken"`
	TokenType        string `json:"tokenType"`
}

// client carries the settings every request needs. It is the seam that reauth
// and doRequest share.
type client struct {
	http      *http.Client
	baseURL   string
	authnPath string
	email     string
	secret    string
	stderr    io.Writer
}

// run builds the command tree and parses/executes args, reading configuration
// from flags or EARL_-prefixed environment variables. It prints help on
// -h/--help and prints the error plus usage on failure, returning the error so
// main can set the process exit code. It never calls os.Exit, so it is safe to
// call from tests.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	rootFlags := ff.NewFlagSet("earl")
	baseURL := rootFlags.StringLong("base-url", "http://localhost:9987", "base URL of the game-server (env EARL_BASE_URL)")
	authnPath := rootFlags.StringLong("authn", "", "path to a JSON file holding the admin session tokens (env EARL_AUTHN)")
	email := rootFlags.StringLong("authn-email", "", "login username used to re-authenticate when the token can't be refreshed (env EARL_AUTHN_EMAIL)")
	secret := rootFlags.StringLong("authn-secret", "", "login password used to re-authenticate when the token can't be refreshed (env EARL_AUTHN_SECRET)")

	root := &ff.Command{
		Name:      "earl",
		Usage:     "earl [FLAGS] <METHOD> <PATH> [BODY]",
		ShortHelp: "smoke-test the game API with an auto-attached bearer token",
		LongHelp: "earl issues an authenticated HTTP request against the game-server. The\n" +
			"bearer token is read from the --authn JSON file and attached automatically.\n" +
			"PATH is joined to --base-url, so `earl get /me` requests ${EARL_BASE_URL}/me.\n" +
			"An optional BODY argument follows PATH: if it names an existing file the\n" +
			"file is sent, `-` reads stdin, and anything else is sent as a literal body.\n" +
			"On success earl prints the HTTP status and the body.\n" +
			"\n" +
			"When there is no --authn file, earl sends the request without a bearer\n" +
			"token — useful for public endpoints like /auth/login. When a token is sent\n" +
			"and rejected (401), earl refreshes it with the stored refresh token — or,\n" +
			"failing that, logs in fresh with --authn-email/--authn-secret — rewrites the\n" +
			"authn file, and retries the request once.",
		Flags: rootFlags,
	}

	// One command per HTTP method keeps the CLI discoverable (`earl get /me`)
	// and lets ff render per-method help. They all share the same handler.
	for _, method := range []string{"get", "post", "put", "patch", "delete"} {
		method := method
		cmd := &ff.Command{
			Name:      method,
			Usage:     "earl " + method + " <PATH> [BODY]",
			ShortHelp: strings.ToUpper(method) + " the given path",
			Flags:     ff.NewFlagSet(method).SetParent(rootFlags),
			Exec: func(ctx context.Context, args []string) error {
				c := &client{
					http:      &http.Client{Timeout: 30 * time.Second},
					baseURL:   *baseURL,
					authnPath: *authnPath,
					email:     *email,
					secret:    *secret,
					stderr:    stderr,
				}
				return c.doRequest(ctx, stdout, strings.ToUpper(method), args)
			},
		}
		root.Subcommands = append(root.Subcommands, cmd)
	}

	switch err := root.ParseAndRun(ctx, args, ff.WithEnvVarPrefix("EARL")); {
	case err == nil:
		return nil
	case errors.Is(err, ff.ErrHelp):
		fmt.Fprintf(stderr, "%s\n", ffhelp.Command(root))
		return nil
	default:
		fmt.Fprintf(stderr, "%s\n", ffhelp.Command(root))
		fmt.Fprintf(stderr, "error: %v\n", err)
		return err
	}
}

// doRequest resolves the token, sends the request, and prints the status line
// and response body. On a 401 it re-authenticates (see reauth), rewrites the
// authn file, and retries once. Any non-2xx status is still printed (that's
// the point of a smoke test); only transport-level failures return an error.
func (c *client) doRequest(ctx context.Context, stdout io.Writer, method string, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("%s requires a PATH argument", strings.ToLower(method))
	}
	if len(args) > 2 {
		return fmt.Errorf("too many arguments: want <PATH> [BODY]")
	}
	path := args[0]

	var body []byte
	if len(args) == 2 {
		b, err := resolveBody(args[1])
		if err != nil {
			return err
		}
		body = b
	}

	// A missing authn file is not an error: earl sends the request without a
	// bearer token, which is what public endpoints (e.g. /auth/login) need. A
	// present-but-broken file still fails loudly.
	a, haveToken, err := loadAuthn(c.authnPath)
	if err != nil {
		return err
	}

	url := strings.TrimRight(c.baseURL, "/") + "/" + strings.TrimLeft(path, "/")

	status, statusLine, respBody, err := c.send(ctx, method, url, body, a.AccessToken)
	if err != nil {
		return err
	}

	// Only treat a 401 as "my token expired" when we actually sent a token and
	// the endpoint reads it. Without a token, a 401 is the endpoint legitimately
	// rejecting the request and must be reported as-is. The /auth/* endpoints
	// ignore the bearer token entirely (they authenticate via the body), so a
	// 401 there means bad credentials/refresh token — refreshing and retrying
	// would be pointless and misleading, so skip it.
	if status == http.StatusUnauthorized && haveToken && !isAuthEndpoint(path) {
		token, source, rerr := c.reauth(ctx, a)
		if rerr != nil {
			fmt.Fprintf(c.stderr, "earl: access token rejected and re-auth failed: %v\n", rerr)
		} else {
			fmt.Fprintf(c.stderr, "earl: access token rejected; re-authenticated via %s\n", source)
			if status, statusLine, respBody, err = c.send(ctx, method, url, body, token); err != nil {
				return err
			}
		}
	}

	fmt.Fprintln(stdout, statusLine)
	if len(respBody) > 0 {
		fmt.Fprintln(stdout, prettyJSON(respBody))
	}
	return nil
}

// send issues one request with the given bearer token and returns the status
// code, status line ("200 OK"), and body. It does not interpret the status.
func (c *client) send(ctx context.Context, method, url string, body []byte, token string) (int, string, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return 0, "", nil, fmt.Errorf("build request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, "", nil, fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", nil, fmt.Errorf("read response body: %w", err)
	}
	return resp.StatusCode, resp.Status, respBody, nil
}

// reauth mints a fresh access token after a 401. It tries the stored refresh
// token first, then falls back to logging in with --authn-email/--authn-secret.
// On success it rewrites the authn file and returns the new access token plus a
// label ("refresh" or "login") for the diagnostic line.
func (c *client) reauth(ctx context.Context, current authn) (token, source string, err error) {
	var refreshErr error
	if current.RefreshToken != "" {
		tokens, rerr := c.authPost(ctx, "/auth/refresh", map[string]string{"refreshToken": current.RefreshToken})
		if rerr == nil {
			if werr := saveAuthn(c.authnPath, tokens); werr != nil {
				return "", "", werr
			}
			return tokens.AccessToken, "refresh", nil
		}
		refreshErr = rerr
	}

	if c.email == "" || c.secret == "" {
		if refreshErr != nil {
			return "", "", fmt.Errorf("refresh failed (%v) and EARL_AUTHN_EMAIL/EARL_AUTHN_SECRET not set for a fresh login", refreshErr)
		}
		return "", "", fmt.Errorf("no refresh token and EARL_AUTHN_EMAIL/EARL_AUTHN_SECRET not set for a fresh login")
	}

	tokens, lerr := c.authPost(ctx, "/auth/login", map[string]string{"username": c.email, "password": c.secret})
	if lerr != nil {
		if refreshErr != nil {
			return "", "", fmt.Errorf("refresh failed (%v) and login failed: %w", refreshErr, lerr)
		}
		return "", "", fmt.Errorf("login failed: %w", lerr)
	}
	if werr := saveAuthn(c.authnPath, tokens); werr != nil {
		return "", "", werr
	}
	return tokens.AccessToken, "login", nil
}

// authPost calls an unauthenticated auth endpoint (/auth/login, /auth/refresh)
// with a JSON body and decodes the AuthTokens response.
func (c *client) authPost(ctx context.Context, path string, reqBody any) (authn, error) {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return authn{}, fmt.Errorf("encode %s request: %w", path, err)
	}
	url := strings.TrimRight(c.baseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return authn{}, fmt.Errorf("build %s request: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return authn{}, fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return authn{}, fmt.Errorf("read %s response: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return authn{}, fmt.Errorf("%s returned %s: %s", path, resp.Status, strings.TrimSpace(string(respBody)))
	}

	var tokens authn
	if err := json.Unmarshal(respBody, &tokens); err != nil {
		return authn{}, fmt.Errorf("decode %s response: %w", path, err)
	}
	if tokens.AccessToken == "" {
		return authn{}, fmt.Errorf("%s response had no accessToken", path)
	}
	return tokens, nil
}

// loadAuthn reads the session tokens from the --authn JSON file. The bool
// reports whether a usable token was loaded. A missing file (or no --authn at
// all) returns (zero, false, nil) so the caller can send an unauthenticated
// request; a present-but-unreadable or malformed file is a hard error.
func loadAuthn(authnPath string) (authn, bool, error) {
	if strings.TrimSpace(authnPath) == "" {
		return authn{}, false, nil
	}
	data, err := os.ReadFile(authnPath)
	if errors.Is(err, os.ErrNotExist) {
		return authn{}, false, nil
	}
	if err != nil {
		return authn{}, false, fmt.Errorf("read authn file: %w", err)
	}
	var a authn
	if err := json.Unmarshal(data, &a); err != nil {
		return authn{}, false, fmt.Errorf("parse authn file %s: %w", authnPath, err)
	}
	if a.AccessToken == "" {
		return authn{}, false, fmt.Errorf("authn file %s has no accessToken", authnPath)
	}
	return a, true, nil
}

// isAuthEndpoint reports whether the path targets an /auth/* endpoint. Those
// endpoints authenticate via the request body rather than the bearer token, so
// earl must not react to their 401s by refreshing and retrying.
func isAuthEndpoint(path string) bool {
	return strings.HasPrefix("/"+strings.TrimLeft(path, "/"), "/auth/")
}

// resolveBody turns the BODY argument into request bytes. It auto-detects the
// source: "-" reads stdin, an argument that names an existing regular file
// sends that file's contents, and anything else is sent verbatim as a literal
// body. The file case is the common one for a smoke tool (bodies live in JSON
// files); the literal case covers quick one-liners like '{"k":"v"}'.
func resolveBody(arg string) ([]byte, error) {
	if arg == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("read body from stdin: %w", err)
		}
		return b, nil
	}
	if fi, err := os.Stat(arg); err == nil && fi.Mode().IsRegular() {
		b, err := os.ReadFile(arg)
		if err != nil {
			return nil, fmt.Errorf("read body file %s: %w", arg, err)
		}
		return b, nil
	}
	return []byte(arg), nil
}

// saveAuthn writes refreshed tokens back to the authn file so the next
// invocation starts from a valid session. The file holds bearer tokens, so it
// is written with owner-only permissions.
func saveAuthn(authnPath string, tokens authn) error {
	data, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return fmt.Errorf("encode authn: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(authnPath, data, 0o600); err != nil {
		return fmt.Errorf("write authn file %s: %w", authnPath, err)
	}
	return nil
}

// readBody returns the request body from a literal argument, or from a file
// when the argument begins with '@' (@- reads stdin).
func readBody(arg string) ([]byte, error) {
	if !strings.HasPrefix(arg, "@") {
		return []byte(arg), nil
	}
	if arg == "@-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("read body from stdin: %w", err)
		}
		return b, nil
	}
	path := strings.TrimPrefix(arg, "@")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read body file: %w", err)
	}
	return b, nil
}

// prettyJSON re-indents a JSON body for readable output, falling back to the
// raw bytes when the body is not valid JSON.
func prettyJSON(b []byte) string {
	var out bytes.Buffer
	if err := json.Indent(&out, b, "", "  "); err != nil {
		return string(b)
	}
	return out.String()
}
