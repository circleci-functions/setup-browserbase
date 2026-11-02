// Package clitest_test exercises the setup-browserbase CLI contract that needs
// no browser: credentials, flags and the checks made before any session is
// opened, against a fake Browserbase API. Hermetic, so it runs under `task
// test`. Browsers are covered by acceptance/.
package clitest_test

import (
	"context"
	"fmt"
	"maps"
	"os"
	"testing"

	"github.com/circleci-functions/toolkit/sdk/sdktest"
	"github.com/circleci-functions/toolkit/testing/compiler"
	"github.com/circleci-functions/toolkit/testing/runner"

	"github.com/circleci-functions/setup-browserbase/internal/testing/fakebrowserbase"
)

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
	p := compiler.NewParallel(1)
	defer p.Cleanup()
	p.Add(compiler.Work{Result: &binaryPath, Name: "setup-browserbase", Target: "..", Source: "."})
	if err := p.Run(context.Background()); err != nil {
		return 0, err
	}
	return m.Run(), nil
}

type env struct {
	sandbox *sdktest.Sandbox
	bb      *fakebrowserbase.Server
	vars    map[string]string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	return &env{
		sandbox: sdktest.NewSandbox(t),
		bb:      fakebrowserbase.New(t),
		vars:    map[string]string{"BROWSERBASE_API_KEY": fakebrowserbase.APIKey},
	}
}

func (e *env) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	vars := e.sandbox.Env()
	maps.Copy(vars, e.vars)
	r := runner.New(t, binaryPath, runner.WithSandboxEnv(vars))
	_, err := r.Run(append(args, "--api-url="+e.bb.URL)...)
	return r.StdoutString() + r.StderrString(), err
}
