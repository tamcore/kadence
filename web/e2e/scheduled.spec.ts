import { expect, test } from '@playwright/test';
import { login } from './helpers';

const USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

test('creates and confirms a static Scheduled reminder', async ({ page }) => {
	await login(page, USERNAME, PASSWORD);
	await page.goto('/scheduled');

	const composer = page.getByLabel('Message');
	await composer.fill('Remind me to drink water tomorrow');
	await composer.press('Enter');

	await expect(page.getByRole('heading', { name: 'Hydration reminder' })).toBeVisible();
	await expect(page.getByText('None — this reminder runs without integrations')).toBeVisible();
	await page.getByRole('button', { name: 'Schedule task' }).click();

	await expect(page).toHaveURL(/\/scheduled\/[0-9a-f-]+$/);
	await expect(page.getByRole('heading', { name: 'Hydration reminder' })).toBeVisible();
	await expect(page.getByText('No runs yet. Use Run now or wait for the first scheduled time.')).toBeVisible();
});

test('refines a Scheduled task one question at a time before confirmation', async ({ page }) => {
	await login(page, USERNAME, PASSWORD);
	await page.goto('/scheduled');

	const composer = page.getByLabel('Message');
	await composer.fill('Help me plan a daily training check-in');
	await composer.press('Enter');

	await expect(
		page.getByRole('heading', { name: 'How often should the check-in run?' })
	).toBeVisible();
	await page.getByRole('button', { name: 'Daily' }).click();

	await expect(page.getByRole('heading', { name: 'Daily training check-in' })).toBeVisible();
	await expect(page.getByText(/Every day/)).toBeVisible();
	await page.getByRole('button', { name: 'Schedule task' }).click();

	await expect(page).toHaveURL(/\/scheduled\/[0-9a-f-]+$/);
	await expect(page.getByRole('heading', { name: 'Daily training check-in' })).toBeVisible();
});
