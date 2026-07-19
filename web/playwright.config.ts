import { defineConfig, devices } from '@playwright/test';

// Playwright config for browser e2e specs against a running Kadence instance.
//
// The app (Go binary + embedded SPA + e2e LLM/embed stub + Postgres) is
// booted externally by scripts/e2e-web.sh (or the CI job that wraps it) —
// this config intentionally has no `webServer` entry. Tests assume the app
// is already reachable at `baseURL` when `npm run e2e` starts.
export default defineConfig({
	testDir: 'e2e',
	fullyParallel: true,
	forbidOnly: !!process.env.CI,
	retries: process.env.CI ? 1 : 0,
	timeout: 30_000,
	expect: {
		timeout: 5_000
	},
	reporter: [['list'], ['html', { open: 'never' }]],
	use: {
		baseURL: process.env.E2E_BASE_URL || 'http://localhost:8080',
		trace: 'on-first-retry'
	},
	projects: [
		{
			name: 'chromium',
			use: { ...devices['Desktop Chrome'] }
		}
	]
});
