package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/circleci-functions/toolkit/sdk"
	"github.com/google/shlex"

	"github.com/circleci-functions/setup-browserbase/browserbase"
	"github.com/circleci-functions/setup-browserbase/fixture"
)

// maxSessions caps sessions: past it, plans' per-minute creation limits make
// startup slower than the parallelism gains.
const maxSessions = 10

type playwrightCmd struct {
	apiFlags     `embed:""`
	sessionFlags `embed:""`

	Sessions   int    `default:"1" help:"Browserbase sessions to run in parallel, one per Playwright worker (1 to 10). The plan limits concurrent sessions: Free allows 3." example:"4"`
	Config     string `help:"Playwright config file. Empty lets Playwright find it." example:"e2e/playwright.config.ts"`
	ProjectDir string `name:"project-dir" default:"." help:"Directory to run Playwright in, holding its node_modules." example:"apps/web"`
	Args       string `help:"More arguments for playwright test." example:"--grep @smoke"`
}

func (c *playwrightCmd) Run(ctx context.Context, client *sdk.Client) error {
	if c.Sessions < 1 || c.Sessions > maxSessions {
		return fmt.Errorf("--sessions=%d: want 1 to %d", c.Sessions, maxSessions)
	}
	extra, err := shlex.Split(c.Args)
	if err != nil {
		return fmt.Errorf("--args: %w", err)
	}
	dir, err := filepath.Abs(c.ProjectDir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "node_modules", "@playwright", "test")); err != nil {
		return fmt.Errorf("@playwright/test is not installed in %s: install the project's dependencies (npm ci, pnpm install, yarn install) before this step", dir)
	}
	if _, err := fixture.Check(dir); err != nil {
		return err
	}
	r, err := c.start(ctx, c.apiFlags)
	if err != nil {
		return err
	}
	defer r.releaseAll(ctx)

	out, err := os.MkdirTemp("", "setup-browserbase-playwright-")
	if err != nil {
		return err
	}
	results := filepath.Join(out, "results")
	report := filepath.Join(out, "report")

	runErr := c.playwright(ctx, r, dir, results, report, extra)

	storeResults(ctx, client, results)
	store(ctx, client, report, "browserbase/playwright-report")
	c.replays(ctx, client, r, out)
	return runErr
}

// playwright runs the project's own Playwright, never a downloaded one, with
// the reporters CircleCI reads added and the fixture's variables set.
func (c *playwrightCmd) playwright(ctx context.Context, r invocation, dir, results, report string, extra []string) error {
	args := []string{"--no-install", "playwright", "test",
		"--workers=" + strconv.Itoa(c.Sessions),
		"--reporter=list,junit,html",
	}
	if c.Config != "" {
		args = append(args, "--config="+c.Config)
	}
	if shard := shardArg(); shard != "" {
		args = append(args, shard)
	}
	args = append(args, extra...)

	headers, _ := json.Marshal(r.headers)
	metadata, _ := json.Marshal(r.metadata)
	cmd := exec.CommandContext(ctx, npx(), args...) //nolint:gosec // the user's own Playwright
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		"BASE_URL="+r.baseURL,
		"BROWSERBASE_API_URL="+c.APIURL,
		"BROWSERBASE_SESSION_TIMEOUT="+strconv.Itoa(int(r.timeout.Seconds())),
		"BROWSERBASE_SESSION_METADATA="+string(metadata),
		"BROWSERBASE_EXTRA_HTTP_HEADERS="+string(headers),
		"PLAYWRIGHT_JUNIT_OUTPUT_FILE="+filepath.Join(results, "playwright.xml"),
		"PLAYWRIGHT_HTML_OUTPUT_DIR="+report,
		"PLAYWRIGHT_HTML_OPEN=never",
		"CI=true",
	)
	// Cancelling the step stops Playwright gently first, so its workers release
	// their sessions; releaseAll catches any that do not.
	cmd.Cancel = func() error { return interrupt(cmd.Process) }
	cmd.WaitDelay = 20 * time.Second

	fmt.Fprintf(os.Stderr, "setup-browserbase: running Playwright against %s on %d Browserbase session(s)\n", r.baseURL, c.Sessions)
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Errorf("playwright test failed (exit %d); results, the report and session replays are stored with the job", exitErr.ExitCode())
	}
	return err
}

// replays stores the replay link of every session this run opened.
func (c *playwrightCmd) replays(ctx context.Context, client *sdk.Client, r invocation, dir string) {
	sessions := r.sessions(ctx)
	if len(sessions) == 0 {
		return
	}
	var sb strings.Builder
	for _, s := range sessions {
		fmt.Fprintln(&sb, browserbase.ReplayURL(s.ID))
	}
	path := filepath.Join(dir, "replays.txt")
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		return
	}
	fmt.Fprintf(os.Stderr, "setup-browserbase: session replays:\n%s", sb.String())
	store(ctx, client, path, "browserbase/replays.txt")
}

// shardArg splits the suite across a job's parallel containers, as CircleCI's
// parallelism key runs the same step in each.
func shardArg() string {
	total, _ := strconv.Atoi(os.Getenv("CIRCLE_NODE_TOTAL"))
	index, err := strconv.Atoi(os.Getenv("CIRCLE_NODE_INDEX"))
	if total < 2 || err != nil {
		return ""
	}
	return fmt.Sprintf("--shard=%d/%d", index+1, total)
}

func npx() string {
	if runtime.GOOS == "windows" {
		return "npx.cmd"
	}
	return "npx"
}

func interrupt(p *os.Process) error {
	if runtime.GOOS == "windows" {
		return p.Kill()
	}
	return p.Signal(syscall.SIGINT)
}
