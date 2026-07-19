import { expect, test } from '@playwright/test';
import { login } from './helpers';

const USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

test('sending a chat message shows the stub assistant reply with no error', async ({ page }) => {
	await login(page, USERNAME, PASSWORD);
	await page.goto('/chat');

	const composer = page.getByLabel('Message');
	await composer.fill('Hello coach');
	await composer.press('Enter');

	// e2e/stub/main.go streams the canned tokens "This is ", "a test ",
	// "coaching reply." — match on the joined text.
	await expect(page.getByText(/test coaching reply/i)).toBeVisible();
	await expect(page.getByRole('alert')).toHaveCount(0);
});
