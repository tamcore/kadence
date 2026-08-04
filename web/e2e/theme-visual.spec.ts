import { expect, test } from '@playwright/test';
import { login } from './helpers';

const ADMIN_USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

// Opt-in: this hits a real deployment and writes image files.
test.skip(!process.env.THEME_VISUAL, 'set THEME_VISUAL=1 to run');

const EXPECTED_BG: Record<string, string> = {
	light: 'rgb(247, 248, 250)',
	dark: 'rgb(20, 23, 27)',
	amoled: 'rgb(0, 0, 0)'
};
const EXPECTED_THEME_COLOR: Record<string, string> = {
	light: '#021c46',
	dark: '#14171b',
	amoled: '#000000'
};
const LABEL: Record<string, string> = {
	light: 'Light',
	dark: 'Dark',
	amoled: 'AMOLED'
};

/** A dev deploy leaves the old shell in the SW cache. Purge before asserting. */
async function purgeServiceWorker(page: import('@playwright/test').Page): Promise<void> {
	await page.goto('/');
	await page.evaluate(async () => {
		const regs = await navigator.serviceWorker?.getRegistrations?.();
		await Promise.all((regs ?? []).map((r) => r.unregister()));
		if (typeof caches !== 'undefined') {
			const keys = await caches.keys();
			await Promise.all(keys.map((k) => caches.delete(k)));
		}
	});
	await page.reload({ waitUntil: 'networkidle' });
}

async function readTheme(page: import('@playwright/test').Page) {
	return page.evaluate(() => ({
		attr: document.documentElement.dataset.theme,
		bg: getComputedStyle(document.body).backgroundColor,
		colorScheme: getComputedStyle(document.documentElement).colorScheme,
		themeColor: document.querySelector('meta[name="theme-color"]')?.getAttribute('content')
	}));
}

test('every theme renders correctly on the deployed build', async ({ page }) => {
	await purgeServiceWorker(page);
	await login(page, ADMIN_USERNAME, ADMIN_PASSWORD);

	for (const theme of ['light', 'dark', 'amoled'] as const) {
		await page.goto('/profile');
		await page.getByRole('radio', { name: LABEL[theme] }).check();

		const state = await readTheme(page);
		expect(state.attr, `${theme}: data-theme`).toBe(theme);
		expect(state.bg, `${theme}: body background`).toBe(EXPECTED_BG[theme]);
		expect(state.themeColor, `${theme}: theme-color meta`).toBe(EXPECTED_THEME_COLOR[theme]);
		expect(state.colorScheme, `${theme}: color-scheme`).toBe(theme === 'light' ? 'light' : 'dark');

		await page.screenshot({ path: `test-results/theme-${theme}-profile.png`, fullPage: true });

		// The surfaces most likely to reveal an unconverted literal.
		for (const [name, path] of [
			['chat', '/'],
			['scheduled', '/scheduled'],
			['mcp', '/mcp'],
			['documents', '/documents']
		] as const) {
			await page.goto(path);
			await page.waitForLoadState('networkidle');
			expect(
				await page.evaluate(() => document.documentElement.dataset.theme),
				`${theme}: ${name} kept the theme`
			).toBe(theme);
			await page.screenshot({ path: `test-results/theme-${theme}-${name}.png`, fullPage: true });
		}
	}
});

test('the theme is applied before the document settles', async ({ page }) => {
	await purgeServiceWorker(page);
	await login(page, ADMIN_USERNAME, ADMIN_PASSWORD);
	await page.goto('/profile');
	await page.getByRole('radio', { name: 'AMOLED' }).check();

	await page.goto('/', { waitUntil: 'commit' });
	const early = await page.evaluate(() => document.documentElement.dataset.theme);
	expect(early, 'bootstrap did not run before the document settled').toBe('amoled');
});
