import { expect, test, type Page, type TestInfo } from '@playwright/test';

test.use({ serviceWorkers: 'block' });

const viewport = { width: 390, height: 844 };
const privateFilename =
	'marathon-training-notes-with-a-very-long-filename-for-mobile-layout-verification.pdf';
const publicFilename =
	'shared-strength-and-recovery-reference-with-a-very-long-filename-for-everyone.pdf';
const serverName = 'endurance-planning-and-recovery-recommendations';
const serverUrl =
	'https://endurance-planning-and-recovery-recommendations.services.example.test/mcp/streamable-http';
const username = 'mobile-layout-verification-user';
const email = 'mobile-layout-verification-user-with-long-address@example.test';
const toolName = 'training_calendar__build_progressive_marathon_recovery_recommendations';
const auditIntent = `Read weather for recovery planning: ${'x'.repeat(512)}`;
const auditGuardReason = `Tool mismatch: ${'x'.repeat(512)}`;

const session = {
	id: 42,
	username: 'admin',
	email: 'admin@example.test',
	role: 'admin' as const,
	displayName: 'Admin',
	unitSystem: 'metric' as const,
	location: 'Berlin',
	aboutMe: '',
	timezone: 'Europe/Berlin',
	scheduledEnabled: false
};

const overview = {
	documentCount: 2,
	documentChunkCount: 2,
	conversationChunkCount: 0,
	documents: [],
	topTerms: [],
	reindex: { stale: 0, total: 0 }
};

const server = {
	id: 7,
	name: serverName,
	transport: 'streamable-http',
	scope: 'user' as const,
	state: 'healthy' as const,
	toolCount: 17,
	url: serverUrl,
	alias: 'training',
	hint: 'Long mobile fixture',
	editable: true
};

const auditCall = {
	id: 42,
	actorUserId: 7,
	actorUsername: username,
	conversationId: '11111111-1111-4111-8111-111111111111',
	source: 'scheduled' as const,
	model: 'openai-compatible-coaching-model-with-a-long-deployment-name',
	toolCallId: 'call-mobile-layout-42',
	toolName,
	status: 'blocked' as const,
	intent: auditIntent,
	guardVerdict: 'denied' as const,
	startedAt: '2026-07-28T18:00:00Z',
	finishedAt: '2026-07-28T18:00:01.234Z'
};

function fixtureData(path: string): unknown {
	switch (path) {
		case '/api/session':
			return session;
		case '/api/context/overview':
			return overview;
		case '/api/conversations':
			return [];
		case '/api/mcp':
			return { servers: [server], canAdd: true };
		case '/api/documents/capabilities':
			return {
				max_bytes: 20 * 1024 * 1024,
				rich_extraction: true,
				accept: 'application/pdf,.pdf,image/png,.png'
			};
		case '/api/documents':
			return [
				{
					id: 1,
					filename: privateFilename,
					mime: 'application/pdf',
					source_type: 'pdf',
					scope: 'private',
					created_at: '2026-07-28T18:00:00Z'
				}
			];
		case '/api/admin/documents':
			return [
				{
					id: 2,
					filename: publicFilename,
					mime: 'application/pdf',
					source_type: 'pdf',
					scope: 'public',
					created_at: '2026-07-28T18:00:00Z'
				}
			];
		case '/api/users':
			return [{ id: 7, username, email, role: 'user' }];
		case '/api/admin/mcp-audit/42':
			return {
				...auditCall,
				arguments: '{"weeks":16,"goal":"sub-4 marathon"}',
				guardReason: auditGuardReason,
				result: '{"recommendation":"easy recovery run"}',
				error: ''
			};
		case '/api/admin/mcp-audit':
			return { items: [auditCall] };
		default:
			return undefined;
	}
}

async function installFixture(page: Page, testInfo: TestInfo): Promise<void> {
	const baseURL = testInfo.project.use.baseURL;
	if (!baseURL) throw new Error('Playwright baseURL is required for mobile list fixtures');
	const appOrigin = new URL(baseURL).origin;

	await page.route(
		(url) => url.origin === appOrigin && url.pathname.startsWith('/api/'),
		async (route) => {
			const request = route.request();
			const path = new URL(request.url()).pathname;
			if (request.method() !== 'GET') {
				await route.continue();
				return;
			}

			const data = fixtureData(path);

			if (data === undefined) {
				await route.continue();
				return;
			}
			await route.fulfill({
				status: 200,
				json: { data },
				headers: path === '/api/session' ? { 'X-CSRF-Token': 'mobile-fixture-token' } : undefined
			});
		}
	);
}

async function openPage(page: Page, testInfo: TestInfo, path: string): Promise<void> {
	await page.setViewportSize(viewport);
	await installFixture(page, testInfo);
	await page.goto(path);
	await expect(page.locator('main')).toBeVisible();
}

async function expectNoHorizontalOverflow(page: Page): Promise<void> {
	const widths = await page.evaluate(() => {
		const main = document.querySelector('main');
		return {
			documentClient: document.documentElement.clientWidth,
			documentScroll: document.documentElement.scrollWidth,
			bodyClient: document.body.clientWidth,
			bodyScroll: document.body.scrollWidth,
			mainClient: main?.clientWidth ?? 0,
			mainScroll: main?.scrollWidth ?? 0
		};
	});
	expect(widths.documentScroll).toBeLessThanOrEqual(widths.documentClient);
	expect(widths.bodyScroll).toBeLessThanOrEqual(widths.bodyClient);
	expect(widths.mainScroll).toBeLessThanOrEqual(widths.mainClient);
}

async function expectCardsInViewport(page: Page, selector: string): Promise<void> {
	const bounds = await page.locator(selector).evaluateAll((elements) =>
		elements.map((element) => {
			const rect = element.getBoundingClientRect();
			return { left: rect.left, right: rect.right, width: rect.width };
		})
	);
	expect(bounds.length).toBeGreaterThan(0);
	for (const bound of bounds) {
		expect(bound.left).toBeGreaterThanOrEqual(0);
		expect(bound.right).toBeLessThanOrEqual(viewport.width);
		expect(bound.width).toBeLessThanOrEqual(viewport.width);
	}
}

test('My documents uses bounded mobile cards and a destructive action menu', async ({
	page
}, testInfo) => {
	await openPage(page, testInfo, '/documents');
	await expect(page.getByText(privateFilename)).toBeVisible();
	await expect(page.locator('input[type="file"]')).toBeVisible();
	await expect(page.getByRole('button', { name: /upload/i })).toBeVisible();
	await expectCardsInViewport(page, 'table tbody tr');
	await expectNoHorizontalOverflow(page);

	await page.getByRole('button', { name: `Actions for ${privateFilename}` }).click();
	await page.getByRole('menuitem', { name: 'Delete' }).click();
	const dialog = page.getByRole('dialog', { name: 'Delete document' });
	await expect(dialog).toContainText(privateFilename);
	await dialog.getByRole('button', { name: 'Cancel' }).click();
	await expect(page.getByText(privateFilename)).toBeVisible();
});

test('Shared knowledge base uses bounded mobile cards and confirms deletion', async ({
	page
}, testInfo) => {
	await openPage(page, testInfo, '/admin/documents');
	await expect(page.getByText(publicFilename)).toBeVisible();
	await expect(page.locator('input[type="file"]')).toBeVisible();
	await expectCardsInViewport(page, 'table tbody tr');
	await expectNoHorizontalOverflow(page);

	await page.getByRole('button', { name: `Actions for ${publicFilename}` }).click();
	await page.getByRole('menuitem', { name: 'Delete' }).click();
	const dialog = page.getByRole('dialog', { name: 'Delete document' });
	await expect(dialog).toContainText(publicFilename);
	await dialog.getByRole('button', { name: 'Cancel' }).click();
});

test('MCP servers keeps long metadata reachable through bounded cards and menus', async ({
	page
}, testInfo) => {
	await openPage(page, testInfo, '/mcp');
	await expect(page.getByText(serverName, { exact: true })).toBeVisible();
	await expect(page.getByText(serverUrl, { exact: true })).toBeVisible();
	await expectCardsInViewport(page, '.list .card');
	await expectNoHorizontalOverflow(page);

	await page.getByRole('button', { name: `Actions for ${serverName}` }).click();
	const menu = page.getByRole('menu', { name: `Actions for ${serverName}` });
	await expect(menu.getByRole('menuitem', { name: 'View' })).toBeVisible();
	await expect(menu.getByRole('menuitem', { name: 'Edit' })).toBeVisible();
	await expect(menu.getByRole('menuitem', { name: 'Delete' })).toBeVisible();
	await menu.getByRole('menuitem', { name: 'Edit' }).click();
	await expect(page.getByLabel('URL', { exact: true })).toHaveValue(serverUrl);
	await page.getByRole('button', { name: 'Cancel' }).click();

	await page.getByRole('button', { name: `Actions for ${serverName}` }).click();
	await page.getByRole('menuitem', { name: 'Delete' }).click();
	const dialog = page.getByRole('dialog', { name: 'Delete MCP server' });
	await expect(dialog).toContainText(serverName);
	await dialog.getByRole('button', { name: 'Cancel' }).click();
});

test('Users keeps long identities reachable through bounded cards and menus', async ({
	page
}, testInfo) => {
	await openPage(page, testInfo, '/admin/users');
	await expect(page.getByText(username, { exact: true })).toBeVisible();
	await expect(page.getByText(email, { exact: true })).toBeVisible();
	await expect(page.getByRole('button', { name: 'New user' })).toBeVisible();
	await expectCardsInViewport(page, 'table tbody tr');
	await expectNoHorizontalOverflow(page);

	await page.getByRole('button', { name: `Actions for ${username}` }).click();
	await page.getByRole('menuitem', { name: 'Edit' }).click();
	await expect(page.getByRole('dialog', { name: 'Edit user' })).toBeVisible();
	await page.getByRole('button', { name: 'Cancel' }).click();

	await page.getByRole('button', { name: `Actions for ${username}` }).click();
	await page.getByRole('menuitem', { name: 'Delete' }).click();
	const dialog = page.getByRole('dialog', { name: 'Delete user' });
	await expect(dialog).toContainText(username);
	await dialog.getByRole('button', { name: 'Cancel' }).click();
});

test('MCP audit keeps filters and long trace fields reachable in bounded cards', async ({
	page
}, testInfo) => {
	await openPage(page, testInfo, '/admin/mcp-audit');
	await expect(page.getByLabel('Tool')).toBeVisible();
	await expect(page.getByLabel('Intent')).toBeVisible();
	await expect(page.getByLabel('Verdict')).toBeVisible();
	await page.getByLabel('Tool').fill(toolName);
	await page.getByLabel('Intent').fill('weather');
	await page.getByLabel('Verdict').selectOption('denied');
	await page.getByLabel('Status').selectOption('blocked');
	await page.getByRole('button', { name: 'Apply filters' }).click();
	await expect(page.getByText(toolName)).toBeVisible();
	await expect(page.getByText(auditCall.model)).toBeVisible();
	await expect(page.getByText(auditIntent)).toBeVisible();
	await expect(page.getByText('denied')).toBeVisible();
	await expectCardsInViewport(page, 'table tbody tr');
	await expectNoHorizontalOverflow(page);

	await page.getByRole('button', { name: 'Actions for call 42' }).click();
	await page.getByRole('menuitem', { name: 'View' }).click();
	const detail = page.getByLabel('MCP call 42 detail');
	await expect(detail).toBeVisible();
	await expect(detail).toContainText(toolName);
	await expect(detail).toContainText(auditGuardReason);
	await expectNoHorizontalOverflow(page);
	await detail.getByRole('button', { name: 'Close detail' }).click();
});
