import { expect, test } from '@playwright/test';
import { login } from './helpers';

const USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

test('sending a chat message shows the stub assistant reply with no error', async ({ page }) => {
	await login(page, USERNAME, PASSWORD);
	await page.goto('/chat');

	const composer = page.getByRole('textbox', { name: 'Message', exact: true });
	await composer.fill('Hello coach');
	await composer.press('Enter');

	// e2e/stub/main.go streams the canned tokens "This is ", "a test ",
	// "coaching reply." — match on the joined text.
	await expect(page.getByText(/test coaching reply/i)).toBeVisible();
	await expect(page.getByRole('alert')).toHaveCount(0);
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
	await regenerate.click();
	await expect(regenerate).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'Regenerate response' })).toHaveCount(1);
	await expect(page.getByRole('alert')).toHaveCount(0);
});

test('keeps the desktop chat thread and composer in a shared 960px column', async ({ page }) => {
	await page.setViewportSize({ width: 1600, height: 1000 });
	await login(page, USERNAME, PASSWORD);
	await page.goto('/chat');

	const thread = page.getByTestId('chat-thread');
	const composer = page.getByTestId('chat-composer');
	await expect(thread).toHaveCSS('max-width', '960px');
	await expect(composer).toHaveCSS('max-width', '960px');
	await expect(thread).toHaveJSProperty('clientWidth', 960);
	await expect(composer).toHaveJSProperty('clientWidth', 960);
});

test('uses content-fit assistant and user bubbles on opposite sides', async ({ page }, testInfo) => {
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
	expect(assistantBox!.width).toBeLessThan(threadBox!.width * 0.8);
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
