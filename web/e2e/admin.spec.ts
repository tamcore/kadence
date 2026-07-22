import { expect, test } from '@playwright/test';
import { login } from './helpers';

const USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

test('admin can create, edit, and delete a user', async ({ page }) => {
	await login(page, USERNAME, PASSWORD);
	await page.goto('/admin/users');

	const newUsername = `e2e-user-${Date.now()}`;

	// Create is behind the "New user" button, which opens a modal.
	await page.getByRole('button', { name: /new user/i }).click();
	await page.getByLabel('Username').fill(newUsername);
	await page.getByLabel('Email').fill(`${newUsername}@example.com`);
	await page.getByLabel('Password').fill('e2e-user-pw-12345');
	await page.getByRole('button', { name: /create user/i }).click();

	const row = page.getByRole('row', { name: new RegExp(newUsername) });
	await expect(row).toBeVisible();

	// Edit the user via the per-row Edit button + modal.
	const newEmail = `${newUsername}-edited@example.com`;
	await row.getByRole('button', { name: /edit/i }).click();
	await page.getByLabel('Email').fill(newEmail);
	await page.getByRole('button', { name: /save changes/i }).click();
	await expect(page.getByRole('row', { name: new RegExp(newEmail) })).toBeVisible();

	// Delete it.
	const editedRow = page.getByRole('row', { name: new RegExp(newUsername) });
	await editedRow.getByRole('button', { name: /delete/i }).click();
	await expect(editedRow).toHaveCount(0);
});
