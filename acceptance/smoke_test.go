//go:build acceptance

package acceptance_test

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestSmoke_PassesAndReports(t *testing.T) {
	e := newEnv(t)

	out, err := e.run(t, "--base-url="+e.site.URL, "--paths=/,/pricing")
	assert.NilError(t, err, out)

	vars := e.exported(t)
	assert.Check(t, cmp.Equal(vars["BROWSERBASE_FAILED_PATHS"], ""))
	assert.Check(t, cmp.Equal(vars["BROWSERBASE_REPLAY_URL"], "https://browserbase.com/sessions/sess_1"))
	assert.Check(t, cmp.Len(e.sandbox.Cache.StoredTestResultPaths(), 1))
	stored := e.sandbox.Cache.StoredArtifacts()
	assert.Assert(t, cmp.Len(stored, 1))
	assert.Check(t, cmp.Equal(stored[0].Destination, "browserbase/screenshots"))

	sessions := e.bb.Sessions()
	assert.Assert(t, cmp.Len(sessions, 1))
	assert.Check(t, cmp.Equal(sessions[0].Status, "COMPLETED"), "the session was not released")
	assert.Check(t, cmp.Equal(sessions[0].Timeout, 1800))
}

func TestSmoke_FailsOnAnHTTPError(t *testing.T) {
	e := newEnv(t)

	out, err := e.run(t, "--base-url="+e.site.URL, "--paths=/,/missing")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "/missing -> 404"))
	assert.Check(t, cmp.Contains(out, "1 of 2 page(s) failed: /missing"))
	assert.Check(t, cmp.Equal(e.exported(t)["BROWSERBASE_FAILED_PATHS"], "/missing"))
	assert.Check(t, cmp.Equal(e.bb.Sessions()[0].Status, "COMPLETED"))
}

func TestSmoke_ConsoleErrors(t *testing.T) {
	e := newEnv(t)

	out, err := e.run(t, "--base-url="+e.site.URL, "--paths=/noisy")
	assert.NilError(t, err, "console errors only fail a page when asked: %s", out)
	assert.Check(t, cmp.Contains(out, "console: boom from the page"))

	out, err = e.run(t, "--base-url="+e.site.URL, "--paths=/noisy", "--fail-on-console-error")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "1 console error(s): boom from the page"))
}

func TestSmoke_SendsTheVercelBypass(t *testing.T) {
	e := newEnv(t)

	out, err := e.run(t, "--base-url="+e.site.URL, "--paths=/protected")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "/protected -> 401"))

	e.vars["VERCEL_AUTOMATION_BYPASS_SECRET"] = "s3cret"
	out, err = e.run(t, "--base-url="+e.site.URL, "--paths=/protected")
	assert.NilError(t, err, out)
	assert.Check(t, !strings.Contains(out, "s3cret"), "the secret was printed")
}

func TestSmoke_DefaultsToTheVercelDeployment(t *testing.T) {
	e := newEnv(t)
	e.vars["VERCEL_DEPLOYMENT_URL"] = e.site.URL

	out, err := e.run(t)
	assert.NilError(t, err, out)
	assert.Check(t, cmp.Contains(out, "checking 1 page(s) of "+e.site.URL))
}

func TestSmoke_UnreachableSiteFailsBeforeOpeningASession(t *testing.T) {
	e := newEnv(t)

	out, err := e.run(t, "--base-url=http://127.0.0.1:1")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "is unreachable from the job"))
	assert.Check(t, cmp.Len(e.bb.Sessions(), 0))
}
