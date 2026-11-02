package clitest_test

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestMissingAPIKeyFailsFirst(t *testing.T) {
	e := newEnv(t)
	delete(e.vars, "BROWSERBASE_API_KEY")

	out, err := e.run(t, "--base-url=https://example.com")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "BROWSERBASE_API_KEY is not set"))
	assert.Check(t, cmp.Len(e.bb.Sessions(), 0))
}

func TestNoSiteToTest(t *testing.T) {
	e := newEnv(t)

	out, err := e.run(t, "smoke")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "no site to test: set base-url, or run setup-vercel's deploy first"))
}

func TestFlagsAreValidated(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"smoke", "--paths=,,"}, "--paths is empty"},
		{[]string{"smoke", "--page-timeout=soon"}, `--page-timeout="soon": want a positive duration`},
		{[]string{"playwright", "--sessions=11"}, "--sessions=11: want 1 to 10"},
		{[]string{"playwright", "--args=--grep 'unterminated"}, "--args:"},
	}
	for _, tt := range tests {
		out, err := e.run(t, tt.args...)
		assert.Check(t, err != nil, out)
		assert.Check(t, cmp.Contains(out, tt.want))
	}
}

func TestPlaywrightNeedsTheProjectsDependencies(t *testing.T) {
	e := newEnv(t)

	out, err := e.run(t, "playwright", "--project-dir="+t.TempDir(), "--base-url=https://example.com")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "@playwright/test is not installed in"))
}

func TestPlaywrightNeedsTheFixture(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	assert.NilError(t, os.MkdirAll(filepath.Join(dir, "node_modules", "@playwright", "test"), 0o750))

	out, err := e.run(t, "playwright", "--project-dir="+dir, "--base-url=https://example.com")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "no setup-browserbase fixture in the project"))
	assert.Check(t, cmp.Len(e.bb.Sessions(), 0))
}
