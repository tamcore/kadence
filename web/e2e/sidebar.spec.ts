import { expect, test, type Page, type TestInfo } from '@playwright/test';
import type { Conversation } from '../src/lib/types';

const session = {
	id: 42,
	username: 'sidebar-runner',
	email: 'sidebar-runner@example.test',
	role: 'user' as const,
	displayName: 'Sidebar Runner',
	unitSystem: 'metric' as const,
	location: 'Berlin',
	aboutMe: 'Testing navigation',
	timezone: 'Europe/Berlin',
	scheduledEnabled: false
};

const overview = {
	documentCount: 0,
	documentChunkCount: 0,
	conversationChunkCount: 0,
	documents: [],
	topTerms: [],
	reindex: { stale: 0, total: 0 }
};

const mcp = {
	canAdd: true,
	servers: [
		{
			id: 7,
			name: 'sidebar-weather',
			transport: 'streamable-http',
			scope: 'global' as const,
			state: 'healthy' as const,
			toolCount: 3,
			checkedAt: '2026-07-26T09:00:00Z',
			error: '',
			url: 'https://mcp.example.test/weather',
			alias: 'Weather',
			hint: 'Forecasts',
			editable: false
		}
	]
};

function conversation(
	id: string,
	title: string,
	lastActivityAt: string,
	pinnedAt: string | null = null
): Conversation {
	return {
		id,
		title,
		createdAt: '2026-07-01T09:00:00Z',
		lastActivityAt,
		pinnedAt
	};
}

const pinnedConversations = [
	conversation('pin-new', 'Pinned today', '2026-07-26T11:00:00Z', '2026-07-26T10:00:00Z'),
	conversation('pin-old', 'Pinned earlier', '2026-07-25T11:00:00Z', '2026-07-25T10:00:00Z')
];

const recentConversations = Array.from({ length: 22 }, (_, index) =>
	conversation(
		`recent-${String(index + 1).padStart(2, '0')}`,
		index === 0 ? 'Recent first' : `Recent ${String(index + 1).padStart(2, '0')}`,
		`2026-07-${String(25 - index).padStart(2, '0')}T12:00:00Z`
	)
);

function crowdedConversations(): Conversation[] {
	return [...pinnedConversations, ...recentConversations].map((item) => ({ ...item }));
}

function sortConversations(items: Conversation[]): Conversation[] {
	return [...items].sort((a, b) => {
		if (a.pinnedAt && b.pinnedAt) return b.pinnedAt.localeCompare(a.pinnedAt);
		if (a.pinnedAt) return -1;
		if (b.pinnedAt) return 1;
		return b.lastActivityAt.localeCompare(a.lastActivityAt);
	});
}

interface SidebarFixture {
	conversations?: Conversation[];
}

async function installSidebarFixture(
	page: Page,
	testInfo: TestInfo,
	fixture: SidebarFixture = {}
): Promise<void> {
	const baseURL = testInfo.project.use.baseURL;
	if (!baseURL) throw new Error('Playwright baseURL is required for scoped API fixtures');
	const appOrigin = new URL(baseURL).origin;
	let items = sortConversations(fixture.conversations ?? crowdedConversations());

	await page.route(
		(url) => url.origin === appOrigin && url.pathname.startsWith('/api/'),
		async (route) => {
			const request = route.request();
			const url = new URL(request.url());
			const path = url.pathname;
			const conversationMatch = path.match(/^\/api\/conversations\/([^/]+)$/);

			if (request.method() === 'GET' && path === '/api/session') {
				await route.fulfill({ status: 200, json: { data: session }, headers: { 'X-CSRF-Token': 'fixture-token' } });
				return;
			}
			if (request.method() === 'GET' && path === '/api/context/overview') {
				await route.fulfill({ status: 200, json: { data: overview } });
				return;
			}
			if (request.method() === 'GET' && path === '/api/mcp') {
				await route.fulfill({ status: 200, json: { data: mcp } });
				return;
			}
			if (request.method() === 'GET' && path === '/api/conversations') {
				await route.fulfill({ status: 200, json: { data: items } });
				return;
			}
			if (request.method() === 'GET' && /^\/api\/conversations\/[^/]+\/messages$/.test(path)) {
				await route.fulfill({ status: 200, json: { data: [] } });
				return;
			}
			if (request.method() === 'PATCH' && conversationMatch) {
				const id = decodeURIComponent(conversationMatch[1]);
				const body = request.postDataJSON() as { title?: string; pinned?: boolean };
				const current = items.find((item) => item.id === id);
				if (!current) {
					await route.fulfill({ status: 404, json: { error: 'conversation not found' } });
					return;
				}
				const updated: Conversation = {
					...current,
					...(body.title === undefined ? {} : { title: body.title }),
					...(body.pinned === undefined
						? {}
						: { pinnedAt: body.pinned ? '2026-07-26T12:30:00Z' : null })
				};
				items = sortConversations(items.map((item) => (item.id === id ? updated : item)));
				await route.fulfill({ status: 200, json: { data: updated } });
				return;
			}
			if (request.method() === 'DELETE' && conversationMatch) {
				const id = decodeURIComponent(conversationMatch[1]);
				items = items.filter((item) => item.id !== id);
				await route.fulfill({ status: 200, json: { data: { ok: true } } });
				return;
			}

			await route.fulfill({ status: 404, json: { error: `unhandled fixture request: ${request.method()} ${path}` } });
		}
	);
}

function conversationRow(page: Page, title: string) {
	return page.locator('.conversation-list li').filter({ has: page.getByRole('link', { name: title, exact: true }) });
}

async function openConversationActions(page: Page, title: string) {
	const row = conversationRow(page, title);
	await row.hover();
	const trigger = row.getByRole('button', { name: `${title} actions`, exact: true });
	await trigger.click();
	return page.getByRole('menu', { name: `${title} actions`, exact: true });
}

test('keeps header and account footer visible while the crowded conversation region scrolls', async ({ page }, testInfo) => {
	await page.setViewportSize({ width: 1280, height: 700 });
	await installSidebarFixture(page, testInfo);
	await page.goto('/');

	const sidebar = page.locator('.sidebar');
	const header = sidebar.locator('.sidebar-header');
	const footer = sidebar.locator('.sidebar-footer');
	const scroll = sidebar.locator('.sidebar-scroll');
	await expect(page.getByRole('button', { name: 'Pinned', exact: true })).toBeVisible();
	await expect(scroll).toBeVisible();

	const before = await Promise.all([header.boundingBox(), footer.boundingBox()]);
	const metrics = await scroll.evaluate((element) => ({
		clientHeight: element.clientHeight,
		scrollHeight: element.scrollHeight,
		overflowY: getComputedStyle(element).overflowY
	}));
	expect(metrics.overflowY).toBe('auto');
	expect(metrics.scrollHeight).toBeGreaterThan(metrics.clientHeight);
	await scroll.evaluate((element) => {
		element.scrollTop = element.scrollHeight;
	});
	await expect.poll(() => scroll.evaluate((element) => element.scrollTop)).toBeGreaterThan(0);

	const after = await Promise.all([header.boundingBox(), footer.boundingBox()]);
	expect(before[0]).not.toBeNull();
	expect(before[1]).not.toBeNull();
	expect(after[0]).not.toBeNull();
	expect(after[1]).not.toBeNull();
	expect(after[0]!.y).toBeCloseTo(before[0]!.y, 3);
	expect(after[1]!.y).toBeCloseTo(before[1]!.y, 3);
	expect(await sidebar.evaluate((element) => element.scrollTop)).toBe(0);
});

test('partitions ordered pinned and recent conversations without duplication', async ({ page }, testInfo) => {
	await installSidebarFixture(page, testInfo);
	await page.goto('/');

	const pinned = page.locator('#sidebar-pinned-conversations');
	const recents = page.locator('#sidebar-recent-conversations');
	await expect(pinned.locator('a')).toHaveText(['Pinned today', 'Pinned earlier']);
	await expect(recents.locator('a').first()).toHaveText('Recent first');
	await expect(recents.getByRole('link', { name: 'Pinned today', exact: true })).toHaveCount(0);
	await expect(pinned.getByRole('link', { name: 'Recent first', exact: true })).toHaveCount(0);
});

test('omits the pinned section when the fixture has no pinned conversations', async ({ page }, testInfo) => {
	await installSidebarFixture(page, testInfo, { conversations: recentConversations });
	await page.goto('/');

	await expect(page.getByRole('button', { name: /pinned/i })).toHaveCount(0);
	await expect(page.locator('#sidebar-pinned-conversations')).toHaveCount(0);
	await expect(page.getByRole('button', { name: /recents/i })).toBeVisible();
});

test('persists a collapsed recents section across reload', async ({ page }, testInfo) => {
	await installSidebarFixture(page, testInfo);
	await page.goto('/');

	const recentsToggle = page.getByRole('button', { name: /recents/i });
	await recentsToggle.click();
	await expect(recentsToggle).toHaveAttribute('aria-expanded', 'false');
	await expect(page.locator('#sidebar-recent-conversations')).toHaveCount(0);

	await page.reload();
	await expect(page.getByRole('button', { name: /recents/i })).toHaveAttribute('aria-expanded', 'false');
	await expect(page.locator('#sidebar-recent-conversations')).toHaveCount(0);
});

test('reveals desktop conversation actions on hover and keyboard focus', async ({ page }, testInfo) => {
	await page.setViewportSize({ width: 1280, height: 700 });
	await installSidebarFixture(page, testInfo);
	await page.goto('/');

	const row = conversationRow(page, 'Recent first');
	const actions = row.locator('.row-actions');
	await expect(actions).toHaveCSS('opacity', '0');
	await row.hover();
	await expect(actions).toHaveCSS('opacity', '1');
	await page.mouse.move(1000, 500);
	await row.getByRole('link', { name: 'Recent first', exact: true }).focus();
	await expect(actions).toHaveCSS('opacity', '1');
});

test('keeps the overflow action available for touch navigation', async ({ browser }, testInfo) => {
	const context = await browser.newContext({
		viewport: { width: 390, height: 844 },
		isMobile: true,
		hasTouch: true
	});
	try {
		const page = await context.newPage();
		await installSidebarFixture(page, testInfo);
		await page.goto('/');
		await page.getByRole('button', { name: 'Menu', exact: true }).click();

		const row = conversationRow(page, 'Recent first');
		const actions = row.locator('.row-actions');
		await expect(actions).toHaveCSS('opacity', '1');
		await expect(row.getByRole('button', { name: 'Recent first actions', exact: true })).toBeVisible();
		await expect(row.locator('.pin-action')).toHaveCSS('display', 'none');
	} finally {
		await context.close();
	}
});

test('opens the overflow menu with the keyboard and keeps future actions disabled', async ({ page }, testInfo) => {
	await installSidebarFixture(page, testInfo);
	await page.goto('/');

	const row = conversationRow(page, 'Recent first');
	await row.getByRole('link', { name: 'Recent first', exact: true }).focus();
	const trigger = row.getByRole('button', { name: 'Recent first actions', exact: true });
	await trigger.press('Enter');
	const menu = page.getByRole('menu', { name: 'Recent first actions', exact: true });
	await expect(menu).toBeVisible();
	await expect(menu.getByRole('menuitem', { name: 'Share (coming soon)', exact: true })).toBeDisabled();
	await expect(menu.getByRole('menuitem', { name: 'Archive (coming soon)', exact: true })).toBeDisabled();
	await expect(menu.getByRole('menuitem', { name: 'Rename', exact: true })).toBeFocused();
	await menu.press('End');
	await expect(menu.getByRole('menuitem', { name: 'Delete', exact: true })).toBeFocused();
	await menu.press('Escape');
	await expect(menu).toHaveCount(0);
	await expect(trigger).toBeFocused();
});

test('sends rename, pin, unpin, and delete requests through sidebar controls', async ({ page }, testInfo) => {
	await installSidebarFixture(page, testInfo, {
		conversations: [conversation('mutate-target', 'Morning recovery', '2026-07-26T08:00:00Z')]
	});
	await page.goto('/');

	const renameMenu = await openConversationActions(page, 'Morning recovery');
	await renameMenu.getByRole('menuitem', { name: 'Rename', exact: true }).click();
	const renameRequest = page.waitForRequest(
		(request) => request.method() === 'PATCH' && new URL(request.url()).pathname === '/api/conversations/mutate-target'
	);
	const dialog = page.getByRole('dialog', { name: 'Rename conversation', exact: true });
	await dialog.getByLabel('Title', { exact: true }).fill('Evening recovery');
	await dialog.getByRole('button', { name: 'Save', exact: true }).click();
	expect((await renameRequest).postDataJSON()).toEqual({ title: 'Evening recovery' });
	await expect(page.getByRole('link', { name: 'Evening recovery', exact: true })).toBeVisible();

	let row = conversationRow(page, 'Evening recovery');
	await row.hover();
	const pinRequest = page.waitForRequest(
		(request) => request.method() === 'PATCH' && new URL(request.url()).pathname === '/api/conversations/mutate-target'
	);
	await row.getByRole('button', { name: 'Pin conversation', exact: true }).click();
	expect((await pinRequest).postDataJSON()).toEqual({ pinned: true });
	await expect(page.locator('#sidebar-pinned-conversations').getByRole('link', { name: 'Evening recovery', exact: true })).toBeVisible();

	row = conversationRow(page, 'Evening recovery');
	await row.hover();
	const unpinRequest = page.waitForRequest(
		(request) => request.method() === 'PATCH' && new URL(request.url()).pathname === '/api/conversations/mutate-target'
	);
	await row.getByRole('button', { name: 'Unpin conversation', exact: true }).click();
	expect((await unpinRequest).postDataJSON()).toEqual({ pinned: false });
	await expect(page.locator('#sidebar-recent-conversations').getByRole('link', { name: 'Evening recovery', exact: true })).toBeVisible();

	const deleteMenu = await openConversationActions(page, 'Evening recovery');
	await deleteMenu.getByRole('menuitem', { name: 'Delete', exact: true }).click();
	const deleteRequest = page.waitForRequest(
		(request) => request.method() === 'DELETE' && new URL(request.url()).pathname === '/api/conversations/mutate-target'
	);
	await page.getByRole('dialog', { name: 'Delete conversation', exact: true }).getByRole('button', { name: 'Delete', exact: true }).click();
	expect((await deleteRequest).postData()).toBeNull();
	await expect(page.getByRole('link', { name: 'Evening recovery', exact: true })).toHaveCount(0);
});
