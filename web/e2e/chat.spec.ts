import { expect, test } from '@playwright/test';
import { login } from './helpers';

const USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

test('sending a chat message shows the stub assistant reply with no error', async ({ page }) => {
	await login(page, USERNAME, PASSWORD);
	await page.goto('/chat');

	const composer = page.getByLabel('Message');
	await composer.fill('Hello coach');
	await composer.press('Enter');

	// e2e/stub/main.go streams the canned tokens "This is ", "a test ",
	// "coaching reply." — match on the joined text.
	await expect(page.getByText(/test coaching reply/i)).toBeVisible();
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

test('uses a flat full-width assistant reply and a capped right-aligned user bubble', async ({ page }) => {
	await page.setViewportSize({ width: 1600, height: 1000 });
	await login(page, USERNAME, PASSWORD);
	await page.goto('/chat');

	const composer = page.getByLabel('Message');
	await composer.fill('A long message '.repeat(100));
	await composer.press('Enter');
	await expect(page.getByText(/test coaching reply/i)).toBeVisible();

	const thread = page.getByTestId('chat-thread');
	const assistant = page.getByTestId('chat-message-assistant').last();
	const user = page.getByTestId('chat-message-user').last();
	const [threadBox, assistantBox, userBox] = await Promise.all([
		thread.boundingBox(),
		assistant.boundingBox(),
		user.boundingBox()
	]);

	expect(threadBox).not.toBeNull();
	expect(assistantBox).not.toBeNull();
	expect(userBox).not.toBeNull();
	expect(assistantBox!.width).toBeCloseTo(threadBox!.width, 0);
	expect(userBox!.width).toBeLessThanOrEqual(threadBox!.width * 0.8 + 1);
	expect(userBox!.x + userBox!.width).toBeCloseTo(threadBox!.x + threadBox!.width, 0);
	await expect(assistant).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)');
	await expect(assistant).toHaveCSS('border-top-width', '0px');
});

test('does not overflow horizontally on a narrow mobile viewport', async ({ page }) => {
	await page.setViewportSize({ width: 390, height: 844 });
	await login(page, USERNAME, PASSWORD);
	await page.goto('/chat');

	const thread = page.getByTestId('chat-thread');
	await expect(thread).toBeVisible();

	const longToken = 'x'.repeat(1_000);
	const composer = page.getByLabel('Message');
	await composer.fill(longToken);
	await composer.press('Enter');
	await expect(page.getByTestId('chat-message-user').last()).toHaveText(longToken);

	await expect.poll(() => thread.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
	await expect
		.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
		.toBe(true);
});
