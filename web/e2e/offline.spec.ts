import { expect, test } from '@playwright/test';
import { login } from './helpers';

const USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

test('shows offline banner and blocks the composer when offline, then recovers', async ({
	page,
	context
}) => {
	await login(page, USERNAME, PASSWORD);
	await page.goto('/chat');

	const composer = page.getByRole('textbox', { name: 'Message', exact: true });
	const sendButton = page.getByRole('button', { name: 'Send' });
	await composer.fill('hello while online');

	await context.setOffline(true);

	await expect(page.getByText(/you.re offline/i)).toBeVisible();
	await expect(sendButton).toBeDisabled();

	await context.setOffline(false);

	await expect(page.getByText(/you.re offline/i)).toHaveCount(0);
	await expect(composer).toBeEditable();
});
