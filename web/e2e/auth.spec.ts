import { expect, test } from '@playwright/test';
import { login } from './helpers';

const USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

test('unauthenticated visit to /chat redirects to /login', async ({ page }) => {
	await page.goto('/chat');
	await expect(page).toHaveURL(/\/login/);
});

test('login navigates away from /login, and logout returns to it', async ({ page }) => {
	await login(page, USERNAME, PASSWORD);
	await expect(page).not.toHaveURL(/\/login/);

	await page.getByRole('button', { name: /log out/i }).click();
	await expect(page).toHaveURL(/\/login/);
});
