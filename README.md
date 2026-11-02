# setup-browserbase

A CircleCI function that tests a site in real browsers on [Browserbase](https://www.browserbase.com):
quick smoke checks with no test code, or your own Playwright suite, with results, screenshots and
session replays brought back into the job.

```yaml
functions:
  vercel: github.com/circleci-functions/setup-vercel@v0.2.0
  browserbase: github.com/circleci-functions/setup-browserbase@v0.1.0

jobs:
  preview-and-test:
    docker:
      - image: cimg/node:lts
    steps:
      - checkout
      - vercel/deploy                 # exports VERCEL_DEPLOYMENT_URL
      - browserbase/smoke:            # a bare `- browserbase` step runs smoke too
          with:
            paths: /,/pricing,/login
      - run: npm ci
      - browserbase/playwright:
          with:
            sessions: 4
```

Both commands test `base-url`, which defaults to `VERCEL_DEPLOYMENT_URL`, the variable
[setup-vercel](https://github.com/circleci-functions/setup-vercel) exports. Run
`circleci function add github.com/circleci-functions/setup-browserbase` to write the entry.

## Credentials

| Variable | |
|---|---|
| `BROWSERBASE_API_KEY` | Required. From the Browserbase dashboard |
| `BROWSERBASE_PROJECT_ID` | Optional; Browserbase infers it from the key |
| `VERCEL_AUTOMATION_BYPASS_SECRET` | Optional. For previews behind Vercel Deployment Protection: sent as `x-vercel-protection-bypass` with `x-vercel-set-bypass-cookie`, so multi-page tests stay in |

Put them in a context. Browserbase limits concurrent sessions per organization, shared across its
projects, so a project only CI uses keeps CI from competing with everything else. A missing key
fails the step before any session is opened.

## smoke

Loads each path in one Browserbase session and checks, per page: the document's HTTP status (400 or
more fails it), console errors and uncaught exceptions, and a full-page screenshot. No test code and
no Node.js needed.

| Flag | Default | Description |
|---|---|---|
| `base-url` | `$VERCEL_DEPLOYMENT_URL` | Site to test |
| `paths` | `/` | Paths to load, comma- or newline-separated |
| `fail-on-console-error` | `false` | Fail a page that logs a console error or throws |
| `page-timeout` | `30s` | How long each page may take |
| `session-timeout` | `30m` | Longest the session may run; it is released when the step ends anyway |

Each path is a test case in the job's Tests tab. Screenshots are stored as artifacts under
`browserbase/screenshots`. Later steps get `BROWSERBASE_FAILED_PATHS` (comma-separated, empty when all
pass) and `BROWSERBASE_REPLAY_URL`. A page that lands on Vercel's login fails with the variable to
set.

## playwright

Runs your Playwright suite, with each Playwright worker on its own Browserbase session.

| Flag | Default | Description |
|---|---|---|
| `base-url` | `$VERCEL_DEPLOYMENT_URL` | Exported to the suite as `BASE_URL` |
| `sessions` | `1` | Parallel sessions, one per worker (1 to 10). Free plans allow 3 at once |
| `config` | | Playwright config file; empty lets Playwright find it |
| `project-dir` | `.` | Where Playwright runs, holding `node_modules` |
| `args` | | More arguments for `playwright test`, such as `--grep @smoke` |
| `session-timeout` | `30m` | Longest a session may run |

It runs the project's own `@playwright/test` (install your dependencies first), with the `junit`
and `html` reporters added: results go to the Tests tab, and the HTML report and a list of session
replays are stored as artifacts under `browserbase/`. Under CircleCI `parallelism`, each container
runs its `--shard` of the suite. If tests fail, the step fails after storing all of that.

### The fixture

`@playwright/test` cannot connect to a remote browser over CDP on its own, so tests import `test` and
`expect` from a small fixture instead. Copy [`fixture/browserbase.ts`](fixture/browserbase.ts) into
your tests, and change their imports:

```ts
import { test, expect } from './browserbase';   // was: from '@playwright/test'
```

It has no dependencies. With `BROWSERBASE_API_KEY` unset it is plain Playwright, so the suite still
runs locally. On Browserbase, it:

- opens one session per worker and reuses it across that worker's tests, since sessions are billed
  by the minute with a one-minute minimum;
- uses the session's default context, which is what Browserbase records, and applies the config's
  `baseURL`, `viewport` and `extraHTTPHeaders` to it, which Playwright would not;
- clears cookies, permissions, storage and extra pages between tests, as a fresh context would;
- waits out Browserbase's plan limits (HTTP 429) instead of failing the first worker that hits one;
- links each test to its session's replay in the HTML report, since Playwright cannot record video
  over CDP;
- keeps an idle connection alive past Browserbase's 10-minute CDP timeout.

The step fails before opening any session if a test still imports `test` from `@playwright/test`,
naming the file and the import to use.

Chromium only: Browserbase serves Chromium over CDP.

## Sessions and cleanup

Every session is tagged with the pipeline, job, commit and a per-step ID. When the step ends,
including when tests fail, it releases any of its sessions still running, and only its own, so a
crashed worker does not leave one billing until its timeout. `session-timeout` bounds each one even
if the step is killed outright.

Playwright replaces a worker after a test fails, and the new worker opens a new session, so a run
with failures uses more sessions (each billed at least a minute) than `sessions`.

## Platforms

Linux, macOS and Windows, on amd64 and arm64.

## Releasing

See [RELEASE.md](RELEASE.md).
