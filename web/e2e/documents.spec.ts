import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';
import { login } from './helpers';

const USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

const dirname = path.dirname(fileURLToPath(import.meta.url));
const samplePdfPath = path.join(dirname, 'fixtures', 'sample.pdf');

test('uploading a document shows it in the list, then deleting removes it', async ({ page }) => {
	await login(page, USERNAME, PASSWORD);
	await page.goto('/documents');

	await page.locator('input[type="file"]').setInputFiles(samplePdfPath);
	await page.getByRole('button', { name: /upload/i }).click();

	const row = page.getByRole('row', { name: /sample\.pdf/i });
	await expect(row).toBeVisible();

	// Delete requires confirmation via the ConfirmDialog.
	await row.getByRole('button', { name: /delete/i }).click();
	await page.getByRole('dialog', { name: 'Delete document' }).getByRole('button', { name: 'Delete' }).click();
	await expect(row).toHaveCount(0);
});
