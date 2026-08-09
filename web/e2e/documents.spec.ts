import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test, type Page, type TestInfo } from '@playwright/test';
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

const fixtureSession = {
	id: 78,
	username: 'document-upload-fixture',
	email: 'document-upload-fixture@example.test',
	role: 'admin' as const,
	displayName: 'Document upload fixture',
	unitSystem: 'metric' as const,
	location: '',
	aboutMe: '',
	timezone: 'UTC',
	scheduledEnabled: false
};

function deferred(): { promise: Promise<void>; resolve: () => void } {
	let resolve!: () => void;
	return { promise: new Promise<void>((done) => (resolve = done)), resolve };
}

async function installDelayedUploadFixture(
	page: Page,
	testInfo: TestInfo,
	destination: { path: string; admin: boolean; filename: string }
): Promise<{ requestStarted: Promise<void>; releaseResponse: () => void }> {
	const baseURL = testInfo.project.use.baseURL;
	if (!baseURL) throw new Error('Playwright baseURL is required for document fixtures');
	const appOrigin = new URL(baseURL).origin;
	const documentsAPIPath = `/api${destination.path}`;
	const responseGate = deferred();
	const started = deferred();
	const documents: Array<{
		id: number;
		filename: string;
		mime: string;
		source_type: string;
		scope: 'private' | 'public';
		created_at: string;
	}> = [];

	await page.route(
		(url) => url.origin === appOrigin && url.pathname.startsWith('/api/'),
		async (route) => {
			const request = route.request();
			const path = new URL(request.url()).pathname;
			if (request.method() === 'GET' && path === '/api/session') {
				await route.fulfill({ status: 200, json: { data: fixtureSession }, headers: { 'X-CSRF-Token': 'fixture-token' } });
				return;
			}
			if (request.method() === 'GET' && path === '/api/context/overview') {
				await route.fulfill({ status: 200, json: { data: { reindex: { stale: 0, total: 0 } } } });
				return;
			}
			if (request.method() === 'GET' && path === '/api/mcp') {
				await route.fulfill({ status: 200, json: { data: { servers: [], canAdd: false } } });
				return;
			}
			if (request.method() === 'GET' && path === '/api/conversations') {
				await route.fulfill({ status: 200, json: { data: [] } });
				return;
			}
			if (request.method() === 'GET' && path === '/api/documents/capabilities') {
				await route.fulfill({
					status: 200,
					json: { data: { max_bytes: 10 * 1024 * 1024, rich_extraction: true, accept: 'application/pdf' } }
				});
				return;
			}
			if (request.method() === 'GET' && path === documentsAPIPath) {
				await route.fulfill({ status: 200, json: { data: documents } });
				return;
			}
			if (request.method() === 'POST' && path === documentsAPIPath) {
				started.resolve();
				await responseGate.promise;
				const document = {
					id: 901,
					filename: destination.filename,
					mime: 'application/pdf',
					source_type: 'pdf',
					scope: destination.admin ? ('public' as const) : ('private' as const),
					created_at: '2026-08-09T10:00:00Z'
				};
				documents.push(document);
				await route.fulfill({ status: 201, json: { data: document } });
				return;
			}
			await route.fulfill({ status: 404, json: { error: `unhandled fixture request: ${request.method()} ${path}` } });
		}
	);

	return { requestStarted: started.promise, releaseResponse: responseGate.resolve };
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

for (const destination of [
	{ name: 'private Documents', path: '/documents', admin: false, filename: 'e2e-private-delayed-lifecycle.pdf' },
	{ name: 'Public Docs', path: '/admin/documents', admin: true, filename: 'e2e-public-delayed-lifecycle.pdf' }
]) {
	test(`shows a delayed upload lifecycle in ${destination.name}`, async ({ page }, testInfo) => {
		const fixture = await installDelayedUploadFixture(page, testInfo, destination);
		await page.goto(destination.path);

		await page.locator('input[type="file"]').setInputFiles({
			name: destination.filename,
			mimeType: 'application/pdf',
			buffer: await readFile(samplePdfPath)
		});
		await page.getByRole('button', { name: 'Upload 1 file' }).click();
		await fixture.requestStarted;

		const progress = page.getByRole('dialog', { name: 'Uploading files' });
		await expect(progress).toContainText(destination.filename);
		await expect(progress).toContainText('Uploading…');
		fixture.releaseResponse();
		await expect(progress).toContainText('Done');
		await expect(page.getByRole('row', { name: destination.filename })).toBeVisible();
	});
}
