import { expect, type Page } from '@playwright/test';

/**
 * Logs in via the real /login form and waits for the app to navigate away
 * from /login (either to the default post-login route or a `returnTo`
 * target). Asserts the redirect actually happened so a broken login surfaces
 * immediately in whichever spec calls this, rather than failing later with a
 * confusing error.
 */
export async function login(page: Page, username: string, password: string): Promise<void> {
	await page.goto('/login');

	// The login page uses the shared `Input` component, which renders an
	// implicit label (`<label class="field"><span>Username</span><input .../></label>`).
	// getByLabel should match that; fall back to the name attribute if the
	// implicit-label association doesn't resolve for some reason.
	const usernameField = page.getByLabel('Username').or(page.locator('input[name="username"]'));
	const passwordField = page.getByLabel('Password').or(page.locator('input[name="password"]'));

	await usernameField.fill(username);
	await passwordField.fill(password);
	await page.getByRole('button', { name: /log ?in/i }).click();

	await expect(page).not.toHaveURL(/\/login/);
}
