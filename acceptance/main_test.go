//go:build acceptance

// Package acceptance_test runs setup-browserbase against a real Chromium,
// reached over CDP the way Browserbase serves one, behind a fake Browserbase
// API, so no account is involved. The Playwright tests use the project's real
// @playwright/test from npm. Tagged out of `task test` because it needs Chrome,
// Node.js and the npm registry.
package acceptance_test

import (
	"context"
	"fmt"
	"html"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/bashenv"
	"github.com/circleci-functions/toolkit/sdk"
	"github.com/circleci-functions/toolkit/sdk/sdktest"
	"github.com/circleci-functions/toolkit/testing/compiler"
	"github.com/circleci-functions/toolkit/testing/runner"

	"github.com/circleci-functions/setup-browserbase/internal/testing/fakebrowserbase"
)

// binaryPathEnvVar names a prebuilt setup-browserbase to test, for an executor
// with no Go toolchain to compile one with.
const binaryPathEnvVar = "SETUP_BROWSERBASE_BINARY"

var binaryPath string

func TestMain(m *testing.M) {
	status, err := runTests(m)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	os.Exit(status)
}

func runTests(m *testing.M) (int, error) {
	if bin := os.Getenv(binaryPathEnvVar); bin != "" {
		abs, err := filepath.Abs(bin)
		if err != nil {
			return 0, fmt.Errorf("%s=%q: %w", binaryPathEnvVar, bin, err)
		}
		if _, err := os.Stat(abs); err != nil {
			return 0, fmt.Errorf("%s=%q: %w", binaryPathEnvVar, bin, err)
		}
		binaryPath = abs
		return m.Run(), nil
	}

	p := compiler.NewParallel(1)
	defer p.Cleanup()
	p.Add(compiler.Work{Result: &binaryPath, Name: "setup-browserbase", Target: "..", Source: "."})
	if err := p.Run(context.Background()); err != nil {
		return 0, err
	}
	return m.Run(), nil
}

// env is a test's sandbox, fake Browserbase API pointing at a local Chrome,
// the site under test, and the variables a job would set.
type env struct {
	sandbox *sdktest.Sandbox
	bb      *fakebrowserbase.Server
	site    *httptest.Server
	vars    map[string]string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{
		sandbox: sdktest.NewSandbox(t),
		bb:      fakebrowserbase.New(t),
		site:    httptest.NewServer(site()),
		vars: map[string]string{
			"BROWSERBASE_API_KEY": fakebrowserbase.APIKey,
			"CIRCLE_SHA1":         "abc123",
		},
	}
	t.Cleanup(e.site.Close)
	e.bb.ConnectURL = chrome(t)
	t.Cleanup(func() {
		assert.Check(t, len(e.bb.Unhandled()) == 0, "setup-browserbase called routes the fake does not have: %v", e.bb.Unhandled())
	})
	return e
}

func (e *env) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	full := append(args, "--api-url="+e.bb.URL)
	vars := e.sandbox.Env()
	maps.Copy(vars, e.vars)
	r := runner.New(t, binaryPath, runner.WithSandboxEnv(vars))
	_, err := r.Run(full...)
	return r.StdoutString() + r.StderrString(), err
}

func (e *env) exported(t *testing.T) map[string]string {
	t.Helper()
	vars, err := bashenv.Vars(e.sandbox.BashEnv, map[string]string{})
	assert.NilError(t, err)
	return vars
}

// site serves the pages the tests load.
func site() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html><head><title>home</title></head><body><h1>home</h1></body></html>`)
	})
	mux.HandleFunc("/pricing", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html><body><h1>pricing</h1></body></html>`)
	})
	mux.HandleFunc("/noisy", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html><body><h1>noisy</h1><script>console.error("boom from the page")</script></body></html>`)
	})
	mux.HandleFunc("/protected", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-vercel-protection-bypass") != "s3cret" {
			http.Error(w, "protected", http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `<html><body><h1>let in</h1></body></html>`)
	})
	mux.HandleFunc("/headers", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<html><body><p id="config">%s</p><p id="bypass">%s</p></body></html>`, //nolint:gosec // a test server echoing the test's own headers
			html.EscapeString(r.Header.Get("x-from-config")), html.EscapeString(r.Header.Get("x-vercel-protection-bypass")))
	})
	return mux
}

// chrome starts a headless Chrome with remote debugging, as a Browserbase
// session's browser, and returns its CDP WebSocket URL.
func chrome(t *testing.T) string {
	t.Helper()
	bin := chromePath()
	if bin == "" {
		if sdk.InCircleCIJob() {
			t.Fatal("no Chrome found: set SETUP_BROWSERBASE_CHROME")
		}
		t.Skip("no Chrome found: set SETUP_BROWSERBASE_CHROME")
	}
	// Not t.TempDir: Chrome's helper processes can still be writing the
	// profile when the browser exits, which fails t.TempDir's cleanup.
	profile, err := os.MkdirTemp("", "setup-browserbase-chrome-")
	assert.NilError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, //nolint:gosec // a browser for the test
		"--headless=new", "--remote-debugging-port=0", "--no-first-run", "--no-default-browser-check",
		"--user-data-dir="+profile, "--no-sandbox", "about:blank")
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	assert.NilError(t, cmd.Start())
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
		for range 10 {
			if os.RemoveAll(profile) == nil {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
	})

	// Chrome writes its port and browser path to DevToolsActivePort in the
	// profile. It also logs the URL to stderr, but not on Windows, where Chrome
	// is a GUI program with no stderr attached.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort")) //nolint:gosec // in the test's profile
		if lines := strings.Fields(string(b)); err == nil && len(lines) >= 2 {
			return "ws://127.0.0.1:" + lines[0] + lines[1]
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("Chrome did not write DevToolsActivePort")
	return ""
}

func chromePath() string {
	if p := os.Getenv("SETUP_BROWSERBASE_CHROME"); p != "" {
		return p
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
	case "windows":
		candidates = []string{`C:\Program Files\Google\Chrome\Application\chrome.exe`, `C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`}
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return playwrightChromium()
}

// playwrightChromium finds a Chromium that `playwright install chromium` put
// in its cache, which the smoke jobs use on executors that ship no Chrome.
func playwrightChromium() string {
	home, _ := os.UserHomeDir()
	var pattern string
	switch runtime.GOOS {
	case "darwin":
		pattern = filepath.Join(home, "Library", "Caches", "ms-playwright", "chromium-*", "chrome-mac*", "*.app", "Contents", "MacOS", "*")
	case "windows":
		pattern = filepath.Join(os.Getenv("LOCALAPPDATA"), "ms-playwright", "chromium-*", "chrome-win*", "chrome.exe")
	default:
		pattern = filepath.Join(home, ".cache", "ms-playwright", "chromium-*", "chrome-linux*", "chrome")
	}
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1]
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
