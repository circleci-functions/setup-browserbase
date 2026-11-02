package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/circleci-functions/toolkit/sdk"

	"github.com/circleci-functions/setup-browserbase/browserbase"
	"github.com/circleci-functions/setup-browserbase/junit"
	"github.com/circleci-functions/setup-browserbase/smoke"
)

type smokeCmd struct {
	apiFlags     `embed:""`
	sessionFlags `embed:""`

	Paths              string `default:"/" help:"Paths to load, comma- or newline-separated." example:"/,/pricing,/login"`
	FailOnConsoleError bool   `name:"fail-on-console-error" help:"Fail a page that logs a console error or throws."`
	PageTimeout        string `name:"page-timeout" default:"30s" help:"How long each page may take to load."`
}

func (c *smokeCmd) Run(ctx context.Context, client *sdk.Client) error {
	pageTimeout, err := parseDuration("page-timeout", c.PageTimeout)
	if err != nil {
		return err
	}
	paths := splitList(c.Paths)
	if len(paths) == 0 {
		return errors.New("--paths is empty")
	}
	r, err := c.start(ctx, c.apiFlags)
	if err != nil {
		return err
	}

	session, err := r.api.Create(ctx, browserbase.CreateOptions{ProjectID: r.projectID, Timeout: r.timeout, Metadata: r.metadata})
	if err != nil {
		return fmt.Errorf("open a Browserbase session: %w", err)
	}
	defer r.releaseAll(ctx)
	fmt.Fprintf(os.Stderr, "setup-browserbase: checking %d page(s) of %s; replay: %s\n", len(paths), r.baseURL, browserbase.ReplayURL(session.ID))

	headers := map[string]any{}
	for k, v := range r.headers {
		headers[k] = v
	}
	pages, runErr := smoke.Checker{
		BaseURL:             r.baseURL,
		Headers:             headers,
		Timeout:             pageTimeout,
		FailOnConsoleErrors: c.FailOnConsoleError,
		Log:                 os.Stderr,
	}.Run(ctx, session.ConnectURL, paths)

	failed := c.report(ctx, client, pages, session.ID)
	switch {
	case runErr != nil:
		return runErr
	case len(failed) > 0:
		return fmt.Errorf("%d of %d page(s) failed: %s; replay: %s", len(failed), len(pages), strings.Join(failed, ", "), browserbase.ReplayURL(session.ID))
	}
	return nil
}

// report stores screenshots and JUnit results, exports the outcome, and
// returns the paths that failed.
func (c *smokeCmd) report(ctx context.Context, client *sdk.Client, pages []smoke.Page, sessionID string) []string {
	dir, err := os.MkdirTemp("", "setup-browserbase-smoke-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup-browserbase: could not write results: %v\n", err)
		return failedPaths(pages)
	}
	shots := filepath.Join(dir, "screenshots")
	_ = os.MkdirAll(shots, 0o750)

	suite := junit.Suite{Name: "setup-browserbase smoke"}
	for _, p := range pages {
		if len(p.Screenshot) > 0 {
			_ = os.WriteFile(filepath.Join(shots, screenshotName(p.Path)), p.Screenshot, 0o600)
		}
		tc := junit.Case{Name: p.Path, ClassName: "smoke", Time: p.Duration, Output: fmt.Sprintf("%s -> HTTP %d\nreplay: %s", p.URL, p.Status, browserbase.ReplayURL(sessionID))}
		if p.Err != nil {
			tc.Failure = p.Err.Error()
		}
		suite.Cases = append(suite.Cases, tc)
	}
	results := filepath.Join(dir, "results")
	if err := suite.Write(filepath.Join(results, "smoke.xml")); err == nil {
		storeResults(ctx, client, results)
	}
	store(ctx, client, shots, "browserbase/screenshots")

	failed := failedPaths(pages)
	if err := export(map[string]string{
		"BROWSERBASE_FAILED_PATHS": strings.Join(failed, ","),
		"BROWSERBASE_REPLAY_URL":   browserbase.ReplayURL(sessionID),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "setup-browserbase: could not export the results: %v\n", err)
	}
	return failed
}

func failedPaths(pages []smoke.Page) []string {
	var failed []string
	for _, p := range pages {
		if p.Err != nil {
			failed = append(failed, p.Path)
		}
	}
	return failed
}

// screenshotName turns a path into a file name: / is index.png, /a/b is a_b.png.
func screenshotName(path string) string {
	name := strings.Trim(strings.NewReplacer("/", "_", "?", "_", "&", "_", "=", "_", ":", "_").Replace(path), "_")
	if name == "" {
		name = "index"
	}
	return name + ".png"
}

// splitList splits a comma- or newline-separated flag value.
func splitList(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}
