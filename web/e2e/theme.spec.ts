import { expect, test } from '@playwright/test';
import { login } from './helpers';

const ADMIN_USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

test('theme choice survives a reload', async ({ page }) => {
	await login(page, ADMIN_USERNAME, ADMIN_PASSWORD);
	await page.goto('/profile');

	await page.getByRole('radio', { name: 'AMOLED' }).check();
	await expect(page.locator('html')).toHaveAttribute('data-theme', 'amoled');
	await expect(page.locator('meta[name="theme-color"]')).toHaveAttribute('content', '#000000');

	await page.reload();
	await expect(page.locator('html')).toHaveAttribute('data-theme', 'amoled');
});

test('the sidebar button cycles the theme', async ({ page }) => {
	await login(page, ADMIN_USERNAME, ADMIN_PASSWORD);
	await page.goto('/profile');

	await page.getByRole('radio', { name: 'Light' }).check();
	await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');

	await page.getByRole('button', { name: 'Switch theme to Dark' }).click();
	await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');

	await page.getByRole('button', { name: 'Switch theme to AMOLED' }).click();
	await expect(page.locator('html')).toHaveAttribute('data-theme', 'amoled');
});

test.describe('auto follows a dark operating system', () => {
	test.use({ colorScheme: 'dark' });

	test('resolves to the chosen dark variant', async ({ page }) => {
		await login(page, ADMIN_USERNAME, ADMIN_PASSWORD);
		await page.goto('/profile');

		await page.getByRole('radio', { name: 'Auto' }).check();
		await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');

		await page.getByRole('radio', { name: 'True black' }).check();
		await expect(page.locator('html')).toHaveAttribute('data-theme', 'amoled');
	});
});

test.describe('auto in a light operating system', () => {
	test.use({ colorScheme: 'light' });

	test('resolves to light regardless of the dark variant', async ({ page }) => {
		await login(page, ADMIN_USERNAME, ADMIN_PASSWORD);
		await page.goto('/profile');

		await page.getByRole('radio', { name: 'Auto' }).check();
		await page.getByRole('radio', { name: 'True black' }).check();
		await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
	});
});
