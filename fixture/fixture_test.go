package fixture_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/circleci-functions/setup-browserbase/fixture"
)

func write(t *testing.T, dir, path, body string) {
	t.Helper()
	full := filepath.Join(dir, path)
	assert.NilError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	assert.NilError(t, os.WriteFile(full, []byte(body), 0o600))
}

func TestCheck_FindsTheFixture(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "tests/browserbase.ts", fixture.Source)
	write(t, dir, "tests/home.spec.ts", "import { test, expect } from './browserbase';\n")
	write(t, dir, "node_modules/x/a.spec.ts", "import { test } from '@playwright/test';\n")

	got, err := fixture.Check(dir)
	assert.NilError(t, err)
	assert.Check(t, cmp.Equal(got, filepath.Join(dir, "tests", "browserbase.ts")))
}

func TestCheck_NamesTheImportToChange(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "e2e/support/browserbase.ts", fixture.Source)
	write(t, dir, "e2e/home.spec.ts", "import { test, expect } from '@playwright/test';\n")
	write(t, dir, "e2e/nested/login.test.ts", "import {expect, test} from \"@playwright/test\";\n")
	write(t, dir, "e2e/types.spec.ts", "import { type Page } from '@playwright/test';\n")

	_, err := fixture.Check(dir)
	assert.ErrorContains(t, err, "2 test file(s) import `test` from '@playwright/test'")
	assert.ErrorContains(t, err, "e2e/home.spec.ts: import { test, expect } from './support/browserbase';")
	assert.ErrorContains(t, err, "e2e/nested/login.test.ts: import { test, expect } from '../support/browserbase';")
	assert.Check(t, !strings.Contains(err.Error(), "types.spec.ts"))
}

func TestCheck_NoFixture(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "tests/home.spec.ts", "import { test } from '@playwright/test';\n")

	_, err := fixture.Check(dir)
	assert.ErrorContains(t, err, "no setup-browserbase fixture in the project")
}
