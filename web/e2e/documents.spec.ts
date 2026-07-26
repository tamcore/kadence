import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test, type Page } from '@playwright/test';
import { login } from './helpers';

const USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

const dirname = path.dirname(fileURLToPath(import.meta.url));
const samplePdfPath = path.join(dirname, 'fixtures', 'sample.pdf');

async function dropFile(page: Page, filename: string): Promise<void> {
	await expect(page.locator('input[type="file"]')).toBeVisible();
	const bytes = Array.from(await readFile(samplePdfPath));
	await page.evaluate(
		({ bytes, filename }) => {
			const transfer = new DataTransfer();
			transfer.items.add(
				new File([new Uint8Array(bytes)], filename, { type: 'application/pdf' })
			);
			document.body.dispatchEvent(
				new DragEvent('dragenter', { bubbles: true, cancelable: true, dataTransfer: transfer })
			);
		},
		{ bytes, filename }
	);
	await expect(page.getByRole('status', { name: 'File drop area' })).toBeVisible();
	await page.evaluate(
		({ bytes, filename }) => {
			const transfer = new DataTransfer();
			transfer.items.add(
				new File([new Uint8Array(bytes)], filename, { type: 'application/pdf' })
			);
			document.body.dispatchEvent(
				new DragEvent('drop', { bubbles: true, cancelable: true, dataTransfer: transfer })
			);
		},
		{ bytes, filename }
	);
}

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

for (const destination of [
	{ name: 'private Documents', path: '/documents', filename: 'private-page-drop.pdf' },
	{ name: 'Public Docs', path: '/admin/documents', filename: 'public-page-drop.pdf' }
]) {
	test(`dropping anywhere queues and uploads into ${destination.name}`, async ({ page }) => {
		await login(page, USERNAME, PASSWORD);
		await page.goto(destination.path);

		await dropFile(page, destination.filename);
		await expect(page.getByRole('list', { name: 'Upload queue' })).toContainText(
			destination.filename
		);
		await page.getByRole('button', { name: 'Upload 1 file' }).click();

		const row = page.getByRole('row', { name: new RegExp(destination.filename, 'i') });
		await expect(row).toBeVisible();
	});
}
