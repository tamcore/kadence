import { readFileSync } from 'node:fs';
import { expect, test, type Page, type Route, type TestInfo } from '@playwright/test';
import { login } from './helpers';

const USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';
const tinyPng = Buffer.from(
	'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=',
	'base64'
);
const samplePdf = readFileSync(new URL('./fixtures/sample.pdf', import.meta.url));

const fixtureSession = {
	id: 77,
	username: 'upload-delete-fixture',
	email: 'upload-delete-fixture@example.test',
	role: 'admin' as const,
	displayName: 'Upload delete fixture',
	unitSystem: 'metric' as const,
	location: '',
	aboutMe: '',
	timezone: 'UTC',
	scheduledEnabled: false
};

const fixtureOverview = { reindex: { stale: 0, total: 0 } };

function deferred(): { promise: Promise<void>; resolve: () => void } {
	let resolve!: () => void;
	return { promise: new Promise<void>((done) => (resolve = done)), resolve };
}

async function generatedScreenshot(page: Page): Promise<Buffer> {
	const dataURL = await page.evaluate(() => {
		const canvas = document.createElement('canvas');
		canvas.width = 1206;
		canvas.height = 2622;
		const context = canvas.getContext('2d');
		if (!context) throw new Error('2D canvas is unavailable');

		const gradient = context.createLinearGradient(0, 0, canvas.width, canvas.height);
		gradient.addColorStop(0, '#0b1f3a');
		gradient.addColorStop(0.45, '#4a236a');
		gradient.addColorStop(1, '#f19c79');
		context.fillStyle = gradient;
		context.fillRect(0, 0, canvas.width, canvas.height);
		context.font = '700 44px sans-serif';
		context.fillStyle = '#ffffff';
		context.fillText('Deterministic upload regression', 64, 108);
		for (let row = 0; row < 73; row += 1) {
			for (let column = 0; column < 31; column += 1) {
				const hue = (row * 47 + column * 29) % 360;
				context.fillStyle = `hsla(${hue}, 78%, 64%, 0.72)`;
				context.fillRect(36 + column * 37, 168 + row * 33, 29, 24);
			}
		}
		return canvas.toDataURL('image/png');
	});
	const image = Buffer.from(dataURL.slice(dataURL.indexOf(',') + 1), 'base64');
	expect(image.byteLength).toBeGreaterThan(100_000);
	expect(image.byteLength).toBeLessThan(10 * 1024 * 1024);
	return image;
}

interface FixtureConversation {
	id: string;
	title: string;
	createdAt: string;
	lastActivityAt: string;
	pinnedAt: string | null;
}

interface FixtureMessage {
	id: number;
	role: 'user' | 'assistant';
	content: string;
}

interface ChatFixture {
	conversations: FixtureConversation[];
	messages: Record<string, FixtureMessage[]>;
	onChat?: (route: Route) => Promise<void>;
}

async function installChatFixture(page: Page, testInfo: TestInfo, fixture: ChatFixture): Promise<{
	deleteRequestCount: () => number;
	regenerateRequestCount: () => number;
}> {
	const baseURL = testInfo.project.use.baseURL;
	if (!baseURL) throw new Error('Playwright baseURL is required for chat fixtures');
	const appOrigin = new URL(baseURL).origin;
	let deleteRequests = 0;
	let regenerateRequests = 0;

	await page.route(
		(url) => url.origin === appOrigin && url.pathname.startsWith('/api/'),
		async (route) => {
			const request = route.request();
			const path = new URL(request.url()).pathname;
			const messageMatch = path.match(/^\/api\/conversations\/([^/]+)\/messages\/(\d+)$/);
			if (request.method() === 'GET' && path === '/api/session') {
				await route.fulfill({ status: 200, json: { data: fixtureSession }, headers: { 'X-CSRF-Token': 'fixture-token' } });
				return;
			}
			if (request.method() === 'GET' && path === '/api/context/overview') {
				await route.fulfill({ status: 200, json: { data: fixtureOverview } });
				return;
			}
			if (request.method() === 'GET' && path === '/api/mcp') {
				await route.fulfill({ status: 200, json: { data: { servers: [], canAdd: false } } });
				return;
			}
			if (request.method() === 'GET' && path === '/api/conversations') {
				await route.fulfill({ status: 200, json: { data: fixture.conversations } });
				return;
			}
			if (request.method() === 'GET' && /^\/api\/conversations\/[^/]+\/messages$/.test(path)) {
				const conversationID = decodeURIComponent(path.split('/')[3]);
				await route.fulfill({ status: 200, json: { data: fixture.messages[conversationID] ?? [] } });
				return;
			}
			if (request.method() === 'GET' && path === '/api/documents/capabilities') {
				await route.fulfill({
					status: 200,
					json: { data: { max_bytes: 10 * 1024 * 1024, rich_extraction: true, accept: 'image/png' } }
				});
				return;
			}
			if (request.method() === 'GET' && path === '/api/documents/references') {
				await route.fulfill({ status: 200, json: { data: { own: [], public: [] } } });
				return;
			}
			if (request.method() === 'POST' && path === '/api/chat' && fixture.onChat) {
				await fixture.onChat(route);
				return;
			}
			if (request.method() === 'POST' && /\/regenerate$/.test(path)) {
				regenerateRequests += 1;
				await route.fulfill({ status: 500, json: { error: 'regeneration must not run' } });
				return;
			}
			if (request.method() === 'DELETE' && messageMatch) {
				deleteRequests += 1;
				const conversationID = decodeURIComponent(messageMatch[1]);
				const messageID = Number(messageMatch[2]);
				const messages = fixture.messages[conversationID] ?? [];
				const index = messages.findIndex((message) => message.id === messageID);
				if (index < 0) {
					await route.fulfill({ status: 404, json: { error: 'message missing' } });
					return;
				}
				fixture.messages[conversationID] = messages.slice(0, index);
				const conversationDeleted = fixture.messages[conversationID].length === 0;
				if (conversationDeleted) {
					fixture.conversations = fixture.conversations.filter((item) => item.id !== conversationID);
				}
				await route.fulfill({ status: 200, json: { data: { conversationDeleted } } });
				return;
			}
			await route.fulfill({ status: 404, json: { error: `unhandled fixture request: ${request.method()} ${path}` } });
		}
	);

	return {
		deleteRequestCount: () => deleteRequests,
		regenerateRequestCount: () => regenerateRequests
	};
}

test('sending a chat message shows the stub assistant reply with no error', async ({ page }) => {
	await login(page, USERNAME, PASSWORD);
	await page.goto('/chat');

	const composer = page.getByRole('textbox', { name: 'Message', exact: true });
	await composer.fill('Hello coach');
	await composer.press('Enter');

	// e2e/stub/main.go streams the canned tokens "This is ", "a test ",
	// "coaching reply." — match on the joined text.
	await expect(page.getByText(/test coaching reply/i)).toBeVisible();
	await expect(
		page.locator('.conversation-list li.active').getByRole('link', { name: 'Marathon Pacing Review', exact: true })
	).toBeVisible();
	await expect(page.getByRole('alert')).toHaveCount(0);
});

test('sends and reloads an attachment-only screenshot turn', async ({ page }) => {
	await login(page, USERNAME, PASSWORD);
	await page.goto('/chat');

	await page.locator('input[type="file"]').setInputFiles({
		name: 'chat-screenshot.png',
		mimeType: 'image/png',
		buffer: tinyPng
	});
	await expect(page.getByRole('img', { name: 'Preview chat-screenshot.png' })).toBeVisible();
	await page.getByRole('button', { name: 'Send' }).click();

	await expect(page.getByRole('img', { name: 'chat-screenshot.png' })).toBeVisible();
	await expect(page.getByText(/test coaching reply/i)).toBeVisible();
	await expect(page).toHaveURL(/\/chat\/[^/]+$/);
	await page.reload();
	await expect(
		page.locator('.conversation-list li.active').getByRole('link', { name: 'Marathon Pacing Review', exact: true })
	).toBeVisible();
});

test('keeps a generated screenshot lifecycle visible until upload completion precedes the assistant', async (
	{ page },
	testInfo
) => {
	const releaseChat = deferred();
	const chatReceived = deferred();
	await installChatFixture(page, testInfo, {
		conversations: [],
		messages: {},
		onChat: async (route) => {
			chatReceived.resolve();
			await releaseChat.promise;
			await route.fulfill({
				status: 200,
				contentType: 'text/event-stream',
				body:
					'data: {"type":"upload","fileOrdinal":0,"filename":"generated-upload-lifecycle.png","status":"processing"}\n\n' +
					'data: {"type":"upload","fileOrdinal":0,"filename":"generated-upload-lifecycle.png","status":"done"}\n\n' +
					'data: {"type":"meta","conversationId":"upload-lifecycle","userMessageId":801,"attachments":[{"filename":"generated-upload-lifecycle.png","mime":"image/png","kind":"image","sizeBytes":100001,"imageWidth":1206,"imageHeight":2622,"ordinal":0}]}\n\n' +
					'data: {"type":"token","delta":"Assistant after upload."}\n\n' +
					'data: {"type":"done","assistantMessageId":802,"assistantContent":"Assistant after upload."}\n\n'
			});
		}
	});
	await page.goto('/chat');

	const screenshot = await generatedScreenshot(page);
	await page.evaluate(() => {
		const lifecycle: string[] = [];
		const observe = () => {
			const state = document.querySelector('[role="status"] .state')?.textContent?.trim();
			if (state && lifecycle[lifecycle.length - 1] !== state) lifecycle.push(state);
		};
		const observer = new MutationObserver(observe);
		observer.observe(document.body, { childList: true, subtree: true, characterData: true });
		(window as typeof window & { uploadLifecycleObserver?: MutationObserver; uploadLifecycleStates?: string[] }).uploadLifecycleObserver = observer;
		(window as typeof window & { uploadLifecycleStates?: string[] }).uploadLifecycleStates = lifecycle;
	});
	await page.locator('input[type="file"]').setInputFiles({
		name: 'generated-upload-lifecycle.png',
		mimeType: 'image/png',
		buffer: screenshot
	});
	await page.getByRole('textbox', { name: 'Message', exact: true }).fill('Inspect the generated screenshot.');
	await page.getByRole('button', { name: 'Send' }).click();
	await chatReceived.promise;

	const progress = page.getByRole('dialog', { name: 'Uploading files' });
	await expect(progress).toContainText('generated-upload-lifecycle.png');
	await expect(progress).toContainText('Uploading…');
	releaseChat.resolve();
	await expect(progress).toContainText('Done');
	await expect(page.getByText('Assistant after upload.', { exact: true })).toBeVisible();
	const lifecycle = await page.evaluate(() => {
		const scopedWindow = window as typeof window & {
			uploadLifecycleObserver?: MutationObserver;
			uploadLifecycleStates?: string[];
		};
		scopedWindow.uploadLifecycleObserver?.disconnect();
		return scopedWindow.uploadLifecycleStates ?? [];
	});
	expect(lifecycle).toEqual(['Uploading…', 'Processing…', 'Done']);
	await expect(progress).toHaveCount(0);
});

test('deleting the first user message once clears the conversation and returns to new chat', async (
	{ page },
	testInfo
) => {
	const fixture = await installChatFixture(page, testInfo, {
		conversations: [
			{
				id: 'delete-first',
				title: 'Delete first fixture',
				createdAt: '2026-08-09T09:00:00Z',
				lastActivityAt: '2026-08-09T09:01:00Z',
				pinnedAt: null
			}
		],
		messages: {
			'delete-first': [
				{ id: 101, role: 'user', content: 'Delete this first turn' },
				{ id: 102, role: 'assistant', content: 'This response must disappear too' }
			]
		}
	});
	await page.goto('/chat/delete-first');
	const firstUser = page.getByTestId('chat-message-user').first().locator('..');
	await expect(firstUser).toContainText('Delete this first turn');
	await firstUser.getByRole('button', { name: 'Delete message' }).click();
	const dialog = page.getByRole('dialog', { name: 'Delete this message?' });
	await expect(dialog).toBeVisible();
	await expect(dialog.getByRole('button', { name: 'Delete' })).toBeFocused();
	await page.keyboard.press('Enter');

	await expect.poll(fixture.deleteRequestCount).toBe(1);
	await expect(page.getByTestId('chat-message-user')).toHaveCount(0);
	await expect(page.getByText('This response must disappear too')).toHaveCount(0);
	await expect(page).toHaveURL(/\/chat$/);
	await expect.poll(fixture.regenerateRequestCount).toBe(0);
});

test('deleting a later user message retains the prefix without regenerating and survives reload', async (
	{ page },
	testInfo
) => {
	const fixture = await installChatFixture(page, testInfo, {
		conversations: [
			{
				id: 'delete-later',
				title: 'Delete later fixture',
				createdAt: '2026-08-09T09:00:00Z',
				lastActivityAt: '2026-08-09T09:01:00Z',
				pinnedAt: null
			}
		],
		messages: {
			'delete-later': [
				{ id: 201, role: 'user', content: 'Keep this prefix' },
				{ id: 202, role: 'assistant', content: 'Keep this response' },
				{ id: 203, role: 'user', content: 'Delete this later turn' },
				{ id: 204, role: 'assistant', content: 'Delete this suffix response' }
			]
		}
	});
	await page.goto('/chat/delete-later');
	const laterUser = page.getByTestId('chat-message-user').nth(1).locator('..');
	await expect(laterUser).toContainText('Delete this later turn');
	await laterUser.getByRole('button', { name: 'Delete message' }).click();
	await page.getByRole('dialog', { name: 'Delete this message?' }).getByRole('button', { name: 'Delete' }).click();

	await expect.poll(fixture.deleteRequestCount).toBe(1);
	await expect(page.getByText('Keep this prefix', { exact: true })).toBeVisible();
	await expect(page.getByText('Keep this response', { exact: true })).toBeVisible();
	await expect(page.getByText('Delete this later turn')).toHaveCount(0);
	await expect(page.getByText('Delete this suffix response')).toHaveCount(0);
	await expect.poll(fixture.regenerateRequestCount).toBe(0);
	await page.reload();
	await expect(page.getByText('Keep this prefix', { exact: true })).toBeVisible();
	await expect(page.getByText('Keep this response', { exact: true })).toBeVisible();
	await expect(page.getByText('Delete this later turn')).toHaveCount(0);
	await expect(page.getByText('Delete this suffix response')).toHaveCount(0);
	await expect.poll(fixture.regenerateRequestCount).toBe(0);
});

test('explicitly references private and public documents and preserves them on reload', async ({
	page
}) => {
	await login(page, USERNAME, PASSWORD);

	await page.goto('/documents');
	await page.locator('input[type="file"]').setInputFiles({
		name: 'chat-private-reference.pdf',
		mimeType: 'application/pdf',
		buffer: samplePdf
	});
	await page.getByRole('button', { name: 'Upload 1 file' }).click();
	await expect(page.getByRole('row', { name: /chat-private-reference\.pdf/i })).toBeVisible();

	await page.goto('/admin/documents');
	await page.locator('input[type="file"]').setInputFiles({
		name: 'chat-public-reference.pdf',
		mimeType: 'application/pdf',
		buffer: samplePdf
	});
	await page.getByRole('button', { name: 'Upload 1 file' }).click();
	await expect(page.getByRole('row', { name: /chat-public-reference\.pdf/i })).toBeVisible();

	await page.goto('/chat');
	await page.getByRole('button', { name: 'Reference documents' }).click();
	await page.getByRole('button', { name: 'Add chat-private-reference.pdf' }).click();
	await page.getByRole('button', { name: 'Add chat-public-reference.pdf' }).click();
	await page.getByRole('textbox', { name: 'Message', exact: true }).fill('Use both plans.');
	await page.getByRole('button', { name: 'Send' }).click();

	const references = page.getByRole('list', { name: 'Referenced documents' });
	await expect(references).toContainText('chat-private-reference.pdf');
	await expect(references).toContainText('Private reference');
	await expect(references).toContainText('chat-public-reference.pdf');
	await expect(references).toContainText('Public reference');
	await expect(page.getByText(/test coaching reply/i)).toBeVisible();
	await expect(page).toHaveURL(/\/chat\/[^/]+$/);
	await page.reload();
	await expect(page.getByRole('list', { name: 'Referenced documents' })).toContainText(
		'chat-private-reference.pdf'
	);
	await expect(page.getByRole('list', { name: 'Referenced documents' })).toContainText(
		'chat-public-reference.pdf'
	);
});

test('edits and regenerates persisted chat turns', async ({ page }) => {
	await login(page, USERNAME, PASSWORD);
	await page.goto('/chat');

	const composer = page.getByRole('textbox', { name: 'Message', exact: true });
	await composer.fill('First prompt');
	await composer.press('Enter');
	await expect(page.getByRole('button', { name: 'Regenerate response' })).toHaveCount(1);

	await composer.fill('Later prompt');
	await composer.press('Enter');
	await expect(page.getByRole('button', { name: 'Regenerate response' })).toHaveCount(2);

	const firstUserBlock = page.getByTestId('chat-message-user').first().locator('..');
	await firstUserBlock.getByRole('button', { name: 'Edit message' }).click();
	await page.getByRole('textbox', { name: 'Edit message' }).fill('Edited first prompt');
	await page.getByRole('button', { name: 'Save edit' }).click();
	await expect(page.getByRole('dialog', { name: 'Rewrite this conversation?' })).toBeVisible();
	await page.getByRole('button', { name: 'Edit and continue' }).click();

	await expect(page.getByTestId('chat-message-user')).toHaveCount(1);
	await expect(page.getByTestId('chat-message-user')).toContainText('Edited first prompt');
	await expect(page.getByRole('button', { name: 'Regenerate response' })).toHaveCount(1);

	const regenerate = page.getByRole('button', { name: 'Regenerate response' });
	const regenerated = page.waitForResponse(
		(response) =>
			response.request().method() === 'POST' &&
			/\/api\/conversations\/[^/]+\/messages\/\d+\/regenerate$/.test(response.url())
	);
	await regenerate.click();
	const regeneratedResponse = await regenerated;
	expect(regeneratedResponse.status()).toBe(200);
	await expect(page.getByRole('button', { name: 'Regenerate response' })).toBeEnabled();
	await expect(page.getByRole('alert')).toHaveCount(0);
});

test('keeps the desktop chat thread and composer in a shared 1200px column', async ({ page }) => {
	await page.setViewportSize({ width: 1600, height: 1000 });
	await login(page, USERNAME, PASSWORD);
	await page.goto('/chat');

	const thread = page.getByTestId('chat-thread');
	const composer = page.getByTestId('chat-composer');
	await expect(thread).toHaveCSS('max-width', '1200px');
	await expect(composer).toHaveCSS('max-width', '1200px');
	await expect(thread).toHaveJSProperty('clientWidth', 1200);
	await expect(composer).toHaveJSProperty('clientWidth', 1200);
});

test('spans the assistant full-width and keeps the user bubble content-fit right', async ({ page }, testInfo) => {
	await page.setViewportSize({ width: 1600, height: 1000 });
	const baseURL = testInfo.project.use.baseURL;
	if (!baseURL) throw new Error('Playwright baseURL is required for scoped API fixtures');
	const appOrigin = new URL(baseURL).origin;
	const isAppApi = (url: URL) => url.origin === appOrigin && url.pathname.startsWith('/api/');
	await page.route(isAppApi, async (route) => {
		const request = route.request();
		const path = new URL(request.url()).pathname;
		if (path === '/api/chat') {
			await route.fulfill({
				status: 200,
				contentType: 'text/event-stream',
				body:
					'data: {"type":"meta","conversationId":"geometry-test"}\n\n' +
					'data: {"type":"token","delta":"This is a test coaching reply."}\n\n' +
					'data: {"type":"done"}\n\n'
			});
			return;
		}
		const data =
			path === '/api/session'
				? {
						id: 1,
						username: 'geometry-user',
						email: 'geometry@example.test',
						role: 'user',
						displayName: '',
						unitSystem: 'metric',
						location: '',
						aboutMe: '',
						timezone: 'UTC',
						scheduledEnabled: false
					}
				: path === '/api/context/overview'
					? { reindex: { stale: 0, total: 0 } }
					: path === '/api/mcp'
						? { servers: [], canAdd: false }
						: path === '/api/conversations'
							? []
							: undefined;
		if (data === undefined) {
			await route.fulfill({ status: 404, json: { error: 'not found' } });
			return;
		}
		await route.fulfill({ status: 200, json: { data } });
	});
	await page.goto('/chat');

	const composer = page.getByRole('textbox', { name: 'Message', exact: true });
	await composer.fill('A long message '.repeat(100));
	await composer.press('Enter');
	await expect(page.getByText(/test coaching reply/i)).toBeVisible();
	await expect(page).toHaveURL(/\/chat\/geometry-test$/);

	const thread = page.getByTestId('chat-thread');
	const assistant = page.getByTestId('chat-message-assistant').last();
	const user = page.getByTestId('chat-message-user').last();
	await expect(thread).toBeVisible();
	await expect(assistant).toBeVisible();
	await expect(user).toBeVisible();
	const [threadBox, assistantBox, userBox] = await Promise.all([
		thread.boundingBox(),
		assistant.boundingBox(),
		user.boundingBox()
	]);

	expect(threadBox).not.toBeNull();
	expect(assistantBox).not.toBeNull();
	expect(userBox).not.toBeNull();
	expect(assistantBox!.width).toBeCloseTo(threadBox!.width, 0);
	expect(assistantBox!.x).toBeCloseTo(threadBox!.x, 0);
	expect(userBox!.width).toBeLessThanOrEqual(threadBox!.width * 0.8 + 1);
	expect(userBox!.x + userBox!.width).toBeCloseTo(threadBox!.x + threadBox!.width, 0);
	await expect(assistant).toHaveCSS('background-color', 'rgb(255, 255, 255)');
	await expect(assistant).toHaveCSS('border-top', '1px solid rgb(226, 230, 234)');

	const [assistantStyle, userStyle] = await Promise.all([
		assistant.evaluate((element) => {
			const style = getComputedStyle(element);
			return {
				padding: style.padding,
				borderRadius: style.borderRadius
			};
		}),
		user.evaluate((element) => {
			const style = getComputedStyle(element);
			return {
				padding: style.padding,
				borderRadius: style.borderRadius
			};
		})
	]);
	expect(assistantStyle).toEqual(userStyle);
});

test('does not overflow horizontally on a narrow mobile viewport', async ({ page }) => {
	await page.setViewportSize({ width: 390, height: 844 });
	await login(page, USERNAME, PASSWORD);
	await page.goto('/chat');

	const thread = page.getByTestId('chat-thread');
	await expect(thread).toBeVisible();

	const longToken = 'x'.repeat(1_000);
	const composer = page.getByRole('textbox', { name: 'Message', exact: true });
	await composer.fill(longToken);
	await composer.press('Enter');
	await expect(page.getByTestId('chat-message-user').last()).toHaveText(longToken);

	await expect.poll(() => thread.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
	await expect
		.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
		.toBe(true);
	const rootMetrics = await page.evaluate(() => {
		const root = document.scrollingElement;
		return {
			bodyHeight: document.body.scrollHeight,
			clientHeight: root?.clientHeight,
			scrollHeight: root?.scrollHeight
		};
	});
	expect(rootMetrics.scrollHeight).toBeLessThanOrEqual(rootMetrics.clientHeight!);
	const user = page.getByTestId('chat-message-user').last();
	const [threadBox, userBox] = await Promise.all([thread.boundingBox(), user.boundingBox()]);
	expect(threadBox).not.toBeNull();
	expect(userBox).not.toBeNull();
	expect(userBox!.width).toBeGreaterThan(threadBox!.width * 0.9);
	expect(userBox!.width).toBeLessThanOrEqual(threadBox!.width * 0.95 + 1);
});
