package junit_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/circleci-functions/setup-browserbase/junit"
)

func TestWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results", "smoke.xml")
	err := junit.Suite{Name: "smoke", Cases: []junit.Case{
		{Name: "/", ClassName: "smoke", Time: time.Second},
		{Name: "/missing", ClassName: "smoke", Failure: "HTTP 404"},
	}}.Write(path)
	assert.NilError(t, err)

	b, err := os.ReadFile(path)
	assert.NilError(t, err)
	assert.Check(t, cmp.Contains(string(b), `<testsuite name="smoke" tests="2" failures="1" time="1">`))
	assert.Check(t, cmp.Contains(string(b), `<failure message="HTTP 404">HTTP 404</failure>`))
}
