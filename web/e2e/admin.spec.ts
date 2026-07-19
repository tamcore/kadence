import { expect, test } from '@playwright/test';
import { login } from './helpers';

const USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

test('admin can create a user and then delete it', async ({ page }) => {
	await login(page, USERNAME, PASSWORD);
	await page.goto('/admin/users');

	const newUsername = `e2e-user-${Date.now()}`;

	await page.getByLabel('Username').fill(newUsername);
	await page.getByLabel('Email').fill(`${newUsername}@example.com`);
	await page.getByLabel('Password').fill('e2e-user-pw-12345');
	await page.getByRole('button', { name: /create user/i }).click();

	const row = page.getByRole('row', { name: new RegExp(newUsername) });
	await expect(row).toBeVisible();

	await row.getByRole('button', { name: /delete/i }).click();
	await expect(row).toHaveCount(0);
});
