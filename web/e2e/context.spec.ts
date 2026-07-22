import { expect, test } from '@playwright/test';
import { login } from './helpers';

const USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

// Chat messages get indexed into the knowledge base (topTerms cloud), which
// the context explorer surfaces. Indexing can lag slightly behind the chat
// response, so this polls the /context page instead of asserting on a single
// load.
test('context explorer shows a term from a recent chat and drills down into a snippet', async ({
	page
}) => {
	await login(page, USERNAME, PASSWORD);

	// The context explorer term index tokenizes on letter runs only (digits are
	// treated as separators and dropped — see internal/knowledge/tfidf.go), so a
	// marker with a numeric suffix would never match as a single term. Map
	// Date.now()'s digits to letters (0-9 -> a-j) to keep the marker unique
	// while staying letters-only.
	const uniqueSuffix = String(Date.now())
		.split('')
		.map((digit) => String.fromCharCode('a'.charCodeAt(0) + Number(digit)))
		.join('');
	const marker = `kadenceexplorertoken${uniqueSuffix}`;
	await page.goto('/chat');
	const composer = page.getByLabel('Message');
	await composer.fill(`Please remember this unique word: ${marker}`);
	await composer.press('Enter');
	await expect(page.getByRole('alert')).toHaveCount(0);
	await expect(page.getByText(/test coaching reply/i)).toBeVisible();

	await expect(async () => {
		await page.goto('/context');
		await expect(page.getByRole('button', { name: marker })).toBeVisible();
	}).toPass({ timeout: 30_000 });

	await page.getByRole('button', { name: marker }).click();
	await expect(page.getByRole('heading', { name: new RegExp(marker) })).toBeVisible();
	await expect(page.getByText(marker, { exact: false }).first()).toBeVisible();
});
