import { expect, test } from '@playwright/test';
import { login } from './helpers';

const ADMIN_USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

// These specs create a throwaway user (rather than reusing the shared admin
// account) so revoking sessions / changing the password here can't break
// other specs that log in as admin. WebAuthn ceremonies are out of scope
// (per the task brief) — the default e2e harness has no
// KADENCE_WEBAUTHN_RP_ID configured, so the Passkeys section never renders
// and there is nothing to skip around.

async function createThrowawayUser(
	page: import('@playwright/test').Page
): Promise<{ username: string; password: string }> {
	const username = `e2e-profile-${Date.now()}`;
	const password = 'e2e-profile-pw-12345';

	await login(page, ADMIN_USERNAME, ADMIN_PASSWORD);
	await page.goto('/admin/users');
	await page.getByRole('button', { name: /new user/i }).click();
	await page.getByLabel('Username').fill(username);
	await page.getByLabel('Email').fill(`${username}@example.com`);
	await page.getByLabel('Password').fill(password);
	await page.getByRole('button', { name: /create user/i }).click();
	await expect(page.getByRole('row', { name: new RegExp(username) })).toBeVisible();

	// Log the admin session back out of this page/context so it doesn't
	// interfere with the throwaway user's own session list.
	await page.getByRole('button', { name: /log out/i }).click();
	await expect(page).toHaveURL(/\/login/);

	return { username, password };
}

test('user can change their own password from the profile page', async ({ page }) => {
	const { username, password } = await createThrowawayUser(page);

	await login(page, username, password);
	await page.goto('/profile');

	await page.getByLabel('Current password').fill(password);
	await page.getByLabel('New password').fill('e2e-profile-pw-67890');
	await page.getByRole('button', { name: /change password/i }).click();

	await expect(page.getByText('Password changed')).toBeVisible();
	await expect(page.getByRole('alert')).toHaveCount(0);

	// The new password now works, and the old one no longer does.
	await page.getByRole('button', { name: /log out/i }).click();
	await expect(page).toHaveURL(/\/login/);
	await login(page, username, 'e2e-profile-pw-67890');
	await expect(page).not.toHaveURL(/\/login/);
});

test('user can see and revoke another active session from the profile page', async ({
	page,
	browser
}) => {
	const { username, password } = await createThrowawayUser(page);

	// Session #1: this test's own page.
	await login(page, username, password);

	// Session #2: a second, independent browser context logged in as the
	// same user (simulates "signed in on another device").
	const otherContext = await browser.newContext();
	const otherPage = await otherContext.newPage();
	await login(otherPage, username, password);
	await otherPage.goto('/documents'); // any authenticated page keeps the session "active"

	await page.goto('/profile');
	await expect(page.getByText(/this device/i)).toBeVisible();

	// There should be exactly one non-current session row with a Revoke button.
	const revokeButtons = page.getByRole('button', { name: 'Revoke' });
	await expect(revokeButtons).toHaveCount(1);
	await revokeButtons.first().click();
	await expect(page.getByRole('button', { name: 'Revoke' })).toHaveCount(0);

	// The revoked session is kicked out on its next authenticated request.
	await otherPage.goto('/documents');
	await expect(otherPage).toHaveURL(/\/login/);

	await otherContext.close();
});
