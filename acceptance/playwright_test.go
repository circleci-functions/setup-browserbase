//go:build acceptance

package acceptance_test

import (
	"embed"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/circleci-functions/setup-browserbase/fixture"
)

// The test project is embedded, so the cross-compiled suite carries it to the
// smoke jobs, which have no checkout.
//
//go:embed testdata/playwright
var testdata embed.FS

// project copies the test project and installs its pinned @playwright/test.
// No Playwright browsers are installed: every browser comes over CDP.
func project(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sub, err := fs.Sub(testdata, "testdata/playwright")
	assert.NilError(t, err)
	assert.NilError(t, os.CopyFS(dir, sub))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "tests", "browserbase.ts"), []byte(fixture.Source), 0o600))
	cmd := exec.Command("npm", "install", "--no-audit", "--no-fund", "--loglevel=error")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1")
	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
	return dir
}

func TestPlaywright_RunsTheSuiteOnBrowserbase(t *testing.T) {
	e := newEnv(t)
	dir := project(t)
	e.vars["VERCEL_AUTOMATION_BYPASS_SECRET"] = "s3cret"
	e.vars["EXPECT_BYPASS"] = "s3cret"

	out, err := e.run(t, "playwright", "--project-dir="+dir, "--base-url="+e.site.URL)
	assert.NilError(t, err, out)
	assert.Check(t, containsAll(out, "6 passed", "1 skipped", "session replays:"), out)

	sessions := e.bb.Sessions()
	assert.Assert(t, cmp.Len(sessions, 1), "one session for the one worker")
	assert.Check(t, cmp.Equal(sessions[0].Status, "COMPLETED"))
	ci, _ := sessions[0].Metadata["ci"].(map[string]any)
	assert.Check(t, cmp.Equal(ci["commit"], "abc123"))

	assert.Check(t, cmp.Len(e.sandbox.Cache.StoredTestResultPaths(), 1))
	var dests []string
	for _, a := range e.sandbox.Cache.StoredArtifacts() {
		dests = append(dests, a.Destination)
	}
	assert.Check(t, cmp.DeepEqual(dests, []string{"browserbase/playwright-report", "browserbase/replays.txt"}))
}

func TestPlaywright_FailingTestsStillReport(t *testing.T) {
	e := newEnv(t)
	dir := project(t)
	e.vars["MAKE_ONE_FAIL"] = "1"

	out, err := e.run(t, "playwright", "--project-dir="+dir, "--base-url="+e.site.URL, "--args=--grep 'fails when asked'")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "playwright test failed"))
	assert.Check(t, cmp.Len(e.sandbox.Cache.StoredTestResultPaths(), 1))
	assert.Check(t, cmp.Equal(e.bb.Sessions()[0].Status, "COMPLETED"))
}

func TestPlaywright_ParallelSessions(t *testing.T) {
	e := newEnv(t)
	dir := project(t)
	e.bb.LimitNext(1)

	out, err := e.run(t, "playwright", "--project-dir="+dir, "--base-url="+e.site.URL, "--sessions=2", "--args=--fully-parallel --grep 'relative goto|viewport'")
	assert.NilError(t, err, out)
	assert.Check(t, cmp.Len(e.bb.Sessions(), 2), "one session per worker, after waiting out a plan limit")
}

func TestPlaywright_DirectImportsFailBeforeOpeningASession(t *testing.T) {
	e := newEnv(t)
	dir := project(t)
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "tests", "direct.spec.ts"), []byte("import { test } from '@playwright/test';\n"), 0o600))

	out, err := e.run(t, "playwright", "--project-dir="+dir, "--base-url="+e.site.URL)
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "tests/direct.spec.ts: import { test, expect } from './browserbase';"))
	assert.Check(t, cmp.Len(e.bb.Sessions(), 0))
}
