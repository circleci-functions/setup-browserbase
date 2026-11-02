import { test, expect } from './browserbase';

test('relative goto resolves against baseURL', async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('h1')).toHaveText('home');
});

test('headers from the config and the bypass secret reach the site', async ({ page }) => {
  await page.goto('/headers');
  await expect(page.locator('#config')).toHaveText('yes');
  await expect(page.locator('#bypass')).toHaveText(process.env.EXPECT_BYPASS ?? '');
});

test('viewport from the config applies', async ({ page }) => {
  await page.goto('/');
  expect(await page.evaluate(() => window.innerWidth)).toBe(800);
});

test('a cookie set by one test', async ({ page, context }) => {
  await page.goto('/');
  await context.addCookies([{ name: 'leak', value: '1', url: page.url() }]);
});

test('is gone in the next', async ({ context }) => {
  expect(await context.cookies()).toEqual([]);
});

test('each test links its replay', async ({}, testInfo) => {
  const replay = testInfo.annotations.find((a) => a.type === 'browserbase-replay');
  expect(replay?.description).toMatch(/^https:\/\/browserbase\.com\/sessions\/sess_\d+$/);
});

test('fails when asked to', async ({ page }) => {
  test.skip(!process.env.MAKE_ONE_FAIL);
  await page.goto('/');
  await expect(page.locator('h1')).toHaveText('not home');
});
