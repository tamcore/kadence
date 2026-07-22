import { expect, test } from '@playwright/test';
import { login } from './helpers';

const USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

test('an expired/cleared session redirects to /login with returnTo on next navigation', async ({
	page,
	context
}) => {
	await login(page, USERNAME, PASSWORD);
	await page.goto('/documents');
	await expect(page).not.toHaveURL(/\/login/);

	// Simulate server-side session expiry/invalidation by dropping the
	// session cookie client-side — the root layout's getCurrentUser() check
	// on the next navigation gets a 401 either way.
	await context.clearCookies();

	await page.goto('/documents');
	await expect(page).toHaveURL(/\/login\?returnTo=%2Fdocuments/);

	// Logging back in returns the user to where they were headed.
	await page.getByLabel('Username').fill(USERNAME);
	await page.getByLabel('Password').fill(PASSWORD);
	await page.getByRole('button', { name: /log ?in/i }).click();
	await expect(page).toHaveURL(/\/documents/);
});
