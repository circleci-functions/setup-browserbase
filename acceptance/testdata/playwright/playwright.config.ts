import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  retries: 0,
  use: {
    baseURL: process.env.BASE_URL,
    viewport: { width: 800, height: 600 },
    extraHTTPHeaders: { 'x-from-config': 'yes' },
  },
});
