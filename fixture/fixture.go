// Package fixture holds the Playwright fixture that runs a suite on
// Browserbase, and finds where a project uses it.
package fixture

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Source is the fixture file users commit.
//
//go:embed browserbase.ts
var Source string

// Marker identifies the fixture file in a project.
const Marker = "setup-browserbase:fixture"

var (
	testFile         = regexp.MustCompile(`\.(spec|test)\.[cm]?[jt]sx?$`)
	playwrightImport = regexp.MustCompile(`import\s*\{[^}]*\btest\b[^}]*\}\s*from\s*['"]@playwright/test['"]`)
)

// Check finds the fixture under dir and fails, naming the files and the import
// to change, if any test file still takes `test` from @playwright/test: those
// tests would run in a local browser rather than on Browserbase.
func Check(dir string) (string, error) {
	var fixturePath string
	var direct []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return skipDir(dir, path, d.Name())
		}
		isTest, isSource := classify(d.Name())
		if !isTest && !isSource {
			return nil
		}
		b, err := os.ReadFile(path) //nolint:gosec // a file in the project being tested
		if err != nil {
			return err
		}
		switch {
		case strings.Contains(string(b), Marker) && !isTest:
			fixturePath = path
		case isTest && playwrightImport.Match(b):
			direct = append(direct, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if fixturePath == "" {
		return "", errors.New("no setup-browserbase fixture in the project: copy " +
			"https://github.com/circleci-functions/setup-browserbase/blob/main/fixture/browserbase.ts into it " +
			"and import `test` and `expect` from it in your tests")
	}
	if len(direct) > 0 {
		return fixturePath, directImports(dir, fixturePath, direct)
	}
	return fixturePath, nil
}

// skipDir skips dependencies and hidden directories, but not dir itself.
func skipDir(root, path, name string) error {
	if name == "node_modules" || (strings.HasPrefix(name, ".") && path != root) {
		return filepath.SkipDir
	}
	return nil
}

// classify reports whether a file is a test, or other source the fixture may be in.
func classify(name string) (isTest, isSource bool) {
	if testFile.MatchString(name) {
		return true, false
	}
	for _, ext := range []string{".ts", ".js", ".mjs", ".mts"} {
		if strings.HasSuffix(name, ext) {
			return false, true
		}
	}
	return false, false
}

func directImports(dir, fixturePath string, files []string) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d test file(s) import `test` from '@playwright/test', so they would not run on Browserbase. Import it from the fixture instead:\n", len(files))
	for i, f := range files {
		if i == 5 {
			fmt.Fprintf(&sb, "  ...and %d more\n", len(files)-5)
			break
		}
		rel, _ := filepath.Rel(filepath.Dir(f), strings.TrimSuffix(fixturePath, filepath.Ext(fixturePath)))
		rel = filepath.ToSlash(rel)
		if !strings.HasPrefix(rel, ".") {
			rel = "./" + rel
		}
		shown, _ := filepath.Rel(dir, f)
		fmt.Fprintf(&sb, "  %s: import { test, expect } from '%s';\n", filepath.ToSlash(shown), rel)
	}
	return errors.New(strings.TrimRight(sb.String(), "\n"))
}
