// setup-browserbase:fixture v1
//
// Runs each Playwright worker's tests in its own Browserbase session when
// BROWSERBASE_API_KEY is set, and in a local browser otherwise, so the same
// suite runs on a laptop and in CI. Import `test` and `expect` from this file
// instead of from '@playwright/test'.
//
// From https://github.com/circleci-functions/setup-browserbase. The
// setup-browserbase function sets the variables below in a CircleCI job.
import { test as base, expect, type Browser, type BrowserContext, type Page } from '@playwright/test';

export { expect };

type Session = { id: string; connectUrl: string };

const apiKey = process.env.BROWSERBASE_API_KEY;
const apiURL = (process.env.BROWSERBASE_API_URL ?? 'https://api.browserbase.com').replace(/\/$/, '');

// request calls the Browserbase API, waiting out plan limits (HTTP 429) for up
// to two minutes, as many workers starting at once on a small plan will hit them.
async function request(path: string, body: unknown): Promise<any> {
  const deadline = Date.now() + 120_000;
  for (;;) {
    const res = await fetch(apiURL + path, {
      method: 'POST',
      headers: { 'X-BB-API-Key': apiKey!, 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    if (res.status === 429) {
      const wait = Number(res.headers.get('retry-after') ?? res.headers.get('x-ratelimit-reset') ?? 5) * 1000;
      if (Date.now() + wait < deadline) {
        await new Promise((r) => setTimeout(r, wait));
        continue;
      }
      throw new Error(
        "Browserbase refused the session: the plan's limit on concurrent sessions or new sessions a minute is " +
          'reached (Free: 3 concurrent, 5 a minute). Lower the number of sessions or workers.',
      );
    }
    if (!res.ok) throw new Error(`Browserbase ${path}: HTTP ${res.status} ${await res.text()}`);
    return res.json();
  }
}

async function createSession(): Promise<Session> {
  return request('/v1/sessions', {
    projectId: process.env.BROWSERBASE_PROJECT_ID || undefined,
    // A backstop: the session ends even if nothing releases it.
    timeout: Number(process.env.BROWSERBASE_SESSION_TIMEOUT ?? 1800),
    userMetadata: JSON.parse(process.env.BROWSERBASE_SESSION_METADATA ?? '{}'),
  });
}

async function releaseSession(id: string): Promise<void> {
  await request(`/v1/sessions/${id}`, { status: 'REQUEST_RELEASE' }).catch(() => {});
}

// Headers for every request, such as Vercel's protection bypass.
const sessionHeaders: Record<string, string> = JSON.parse(process.env.BROWSERBASE_EXTRA_HTTP_HEADERS ?? '{}');

const browserbase = base.extend<{ browserbaseReplay: void }, { browserbaseSession: Session }>({
  browserbaseSession: [
    async ({}, use) => {
      const session = await createSession();
      try {
        await use(session);
      } finally {
        await releaseSession(session.id);
      }
    },
    { scope: 'worker' },
  ],

  // Every test links its session's replay, in the HTML report.
  browserbaseReplay: [
    async ({ browserbaseSession }, use, testInfo) => {
      testInfo.annotations.push({ type: 'browserbase-replay', description: `https://browserbase.com/sessions/${browserbaseSession.id}` });
      await use();
    },
    { auto: true },
  ],

  browser: [
    async ({ playwright, browserbaseSession }, use) => {
      const browser: Browser = await playwright.chromium.connectOverCDP(browserbaseSession.connectUrl);
      await use(browser);
      await browser.close();
    },
    { scope: 'worker' },
  ],

  // Browserbase records the session's default context, so tests use it rather
  // than a context of their own. Playwright's `use` options do not apply to a
  // context it did not create, so the ones a suite relies on are applied here,
  // and what one test leaves behind is cleared before the next.
  context: async ({ browser, extraHTTPHeaders, baseURL }, use) => {
    const context: BrowserContext = browser.contexts()[0];
    await context.setExtraHTTPHeaders({ ...sessionHeaders, ...extraHTTPHeaders });
    await use(context);
    for (const page of context.pages().slice(1)) await page.close();
    await context.clearCookies();
    await context.clearPermissions();
    if (baseURL) {
      const page = context.pages()[0];
      await page?.goto(baseURL).then(() => page.evaluate(() => { localStorage.clear(); sessionStorage.clear(); })).catch(() => {});
    }
  },

  page: async ({ context, baseURL, viewport }, use) => {
    const page: Page = context.pages()[0] ?? (await context.newPage());
    if (viewport) await page.setViewportSize(viewport);

    // The default context has no baseURL, so relative paths are resolved here.
    if (baseURL) {
      const goto = page.goto.bind(page);
      page.goto = (url, options) => goto(new URL(url, baseURL).toString(), options);
    }

    // Browserbase drops a CDP connection after 10 minutes with no traffic.
    const heartbeat = setInterval(() => page.evaluate(() => 0).catch(() => {}), 4 * 60_000);
    try {
      await use(page);
    } finally {
      clearInterval(heartbeat);
    }
  },
});

export const test = apiKey ? (browserbase as unknown as typeof base) : base;
