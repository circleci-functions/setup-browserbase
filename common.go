package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/circleci-functions/toolkit/bashenv"
	"github.com/circleci-functions/toolkit/sdk"

	"github.com/circleci-functions/setup-browserbase/browserbase"
)

// The variables setup-browserbase reads.
const (
	envAPIKey       = "BROWSERBASE_API_KEY" //nolint:gosec // the variable's name, not a credential
	envProjectID    = "BROWSERBASE_PROJECT_ID"
	envBypassSecret = "VERCEL_AUTOMATION_BYPASS_SECRET" //nolint:gosec // the variable's name, not a credential
	// envDeploymentURL is what setup-vercel exports after a deploy.
	envDeploymentURL = "VERCEL_DEPLOYMENT_URL"
)

// apiFlags are the test hooks every command takes.
type apiFlags struct {
	APIURL string `name:"api-url" hidden:"" default:"https://api.browserbase.com" help:"Browserbase API base URL."`
}

// sessionFlags are what every command passes to the sessions it opens.
type sessionFlags struct {
	BaseURL        string `name:"base-url" help:"URL of the site to test. Empty uses VERCEL_DEPLOYMENT_URL, which setup-vercel exports." example:"https://my-app.vercel.app"`
	SessionTimeout string `name:"session-timeout" default:"30m" help:"Longest a Browserbase session may run. A backstop: sessions are released when the step ends."`
}

// invocation is one run's view of the job: credentials, the site, and the
// metadata that marks the Browserbase sessions it opens as its own.
type invocation struct {
	api       *browserbase.Client
	projectID string
	baseURL   string
	headers   map[string]string
	timeout   time.Duration
	id        string
	metadata  map[string]any
}

func (f sessionFlags) start(ctx context.Context, api apiFlags) (invocation, error) {
	key := os.Getenv(envAPIKey)
	if key == "" {
		return invocation{}, fmt.Errorf("%s is not set: add it, and optionally %s, to the job, for example from a context. "+
			"A project only CI uses keeps CI sessions out of everyone else's concurrency", envAPIKey, envProjectID)
	}
	timeout, err := parseDuration("session-timeout", f.SessionTimeout)
	if err != nil {
		return invocation{}, err
	}
	base := f.BaseURL
	if base == "" {
		base = os.Getenv(envDeploymentURL)
	}
	if base == "" {
		return invocation{}, fmt.Errorf("no site to test: set base-url, or run setup-vercel's deploy first, which exports %s", envDeploymentURL)
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}

	r := invocation{
		api:       browserbase.New(api.APIURL, key),
		projectID: os.Getenv(envProjectID),
		baseURL:   strings.TrimRight(base, "/"),
		headers:   map[string]string{},
		timeout:   timeout,
		id:        runID(),
	}
	if secret := os.Getenv(envBypassSecret); secret != "" {
		r.headers["x-vercel-protection-bypass"] = secret
		r.headers["x-vercel-set-bypass-cookie"] = "true"
	}
	// Browserbase rejects an empty metadata value, so unset ones are left out.
	ci := map[string]string{"provider": "circleci", "run": r.id}
	for key, name := range map[string]string{"pipeline": "CIRCLE_PIPELINE_ID", "job": "CIRCLE_WORKFLOW_JOB_ID", "commit": "CIRCLE_SHA1"} {
		if v := os.Getenv(name); v != "" {
			ci[key] = v
		}
	}
	r.metadata = map[string]any{"ci": ci}
	return r, r.reachable(ctx)
}

// reachable fails fast, before any session is opened, if the site does not
// answer at all. HTTP errors are left to the browser, which reports them per page.
func (r invocation) reachable(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL, nil)
	if err != nil {
		return fmt.Errorf("base-url %q: %w", r.baseURL, err)
	}
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s is unreachable from the job: %w", r.baseURL, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return nil
}

// query matches this invocation's sessions in Browserbase's list API.
func (r invocation) query() string {
	return fmt.Sprintf("user_metadata['ci']['run']:'%s'", r.id)
}

// sessions lists this invocation's sessions, for replay links.
func (r invocation) sessions(ctx context.Context) []browserbase.Session {
	ss, err := r.api.List(ctx, r.query(), "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup-browserbase: could not list this run's sessions: %v\n", err)
	}
	return ss
}

// releaseAll ends every session of this invocation still running, so a test
// that crashed or a step that was cancelled does not leave them billing until
// their timeout. It runs on a fresh context, since the step's may be cancelled.
func (r invocation) releaseAll(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	ss, err := r.api.List(ctx, r.query(), "RUNNING")
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup-browserbase: could not list this run's sessions to release them: %v\n", err)
		return
	}
	for _, s := range ss {
		if err := r.api.Release(ctx, s.ID, r.projectID); err != nil {
			fmt.Fprintf(os.Stderr, "setup-browserbase: could not release session %s: %v\n", s.ID, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "setup-browserbase: released session %s\n", s.ID)
	}
}

// export passes values to later steps. Outside a job there is nowhere to put them.
func export(vars map[string]string) error {
	err := bashenv.ExportEnvVars(vars)
	if errors.Is(err, bashenv.ErrNoBashEnv) {
		return nil
	}
	return err
}

// store keeps a file or directory as an artifact. Outside a job, or if the
// store fails, it says so and carries on: results are worth more than a failed step.
func store(ctx context.Context, client *sdk.Client, path, destination string) {
	err := client.StoreArtifacts(ctx, path, destination)
	if err != nil && !errors.Is(err, sdk.ErrNoServer) {
		fmt.Fprintf(os.Stderr, "setup-browserbase: could not store %s: %v\n", destination, err)
	}
}

func storeResults(ctx context.Context, client *sdk.Client, path string) {
	err := client.StoreTestResults(ctx, path)
	if err != nil && !errors.Is(err, sdk.ErrNoServer) {
		fmt.Fprintf(os.Stderr, "setup-browserbase: could not store test results: %v\n", err)
	}
}

// parseDuration reads a duration flag. Durations are strings in the config, so
// that the published flag type does not make a config check expect an integer.
func parseDuration(flag, s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("--%s=%q: want a positive duration such as 10m or 90s", flag, s)
	}
	return d, nil
}

func runID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
