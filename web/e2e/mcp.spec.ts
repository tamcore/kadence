import { expect, test } from '@playwright/test';
import { login } from './helpers';

const USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

// Requires the harness to enable user-defined MCP servers: KADENCE_ENCRYPTION_KEY
// + KADENCE_USER_MCP_ALLOWED_HOSTS="*.e2e.test" (set by scripts/e2e-web.sh).
// The server URL never needs to actually resolve — creation only validates
// the URL against the host allowlist; health-checking it afterwards is
// allowed to report unhealthy/checking.
test('user can add, edit, and delete their own MCP server', async ({ page }) => {
	await login(page, USERNAME, PASSWORD);
	await page.goto('/mcp');

	const serverName = `e2e-mcp-${Date.now()}`;

	await page.getByRole('button', { name: /add mcp server/i }).click();
	await page.getByLabel('Name').fill(serverName);
	await page.getByLabel('URL').fill(`https://${serverName}.e2e.test/mcp`);
	await page.getByRole('button', { name: /add server/i }).click();

	const link = page.getByRole('link', { name: new RegExp(serverName) });
	await expect(link).toBeVisible();

	// Edit: the row swaps in the same form, prefilled.
	const card = page.locator('li', { has: link });
	await card.getByRole('button', { name: /edit/i }).click();
	const newUrl = `https://${serverName}-edited.e2e.test/mcp`;
	await page.getByLabel('URL').fill(newUrl);
	await page.getByRole('button', { name: 'Save' }).click();
	await expect(link).toBeVisible();

	// Delete requires confirmation.
	await card.getByRole('button', { name: /delete/i }).click();
	const dialog = page.getByRole('dialog', { name: 'Delete MCP server' });
	await expect(dialog).toBeVisible();
	await dialog.getByRole('button', { name: 'Cancel' }).click();
	await expect(link).toBeVisible();

	await card.getByRole('button', { name: /delete/i }).click();
	await page.getByRole('dialog', { name: 'Delete MCP server' }).getByRole('button', { name: 'Delete' }).click();
	await expect(page.getByRole('link', { name: new RegExp(serverName) })).toHaveCount(0);
});
