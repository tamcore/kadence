import { expect, test, type Page } from '@playwright/test';
import { login } from './helpers';

const USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

async function sendChat(page: Page, message: string): Promise<void> {
	const composer = page.getByRole('textbox', { name: 'Message', exact: true });
	await composer.fill(message);
	await composer.press('Enter');
}

test('a direct scheduled delivery lands in chat, bumps the chat list, and is replyable', async ({ page }) => {
	test.setTimeout(60_000);
	await login(page, USERNAME, PASSWORD);

	// Seed an existing chat first so there is something for the delivery
	// conversation to be bumped above once the scheduled task delivers into
	// its own (confirm-time-promoted) conversation. The e2e suite runs fully
	// parallel against one shared admin account (per playwright.config.ts),
	// so other specs' conversations may also be in Recents; comparing this
	// seed conversation's position against the delivery conversation's
	// (rather than asserting the delivery conversation is globally first)
	// keeps the bump-to-top assertion below immune to that cross-spec noise.
	await page.goto('/chat');
	await sendChat(page, 'Just chatting before the scheduled task runs.');
	await expect(page.getByText(/test coaching reply/i)).toBeVisible();
	await expect(page).toHaveURL(/\/chat\/[^/]+$/);
	const seedChatPath = new URL(page.url()).pathname;

	// 1. Create + confirm a schedule directly from /scheduled (same flow as
	// scheduled.spec.ts's "creates and confirms a static Scheduled reminder").
	await page.goto('/scheduled');
	const scheduledComposer = page.getByLabel('Message');
	await scheduledComposer.fill('Remind me to drink water tomorrow');
	await scheduledComposer.press('Enter');
	await expect(page.getByRole('heading', { name: 'Hydration reminder' })).toBeVisible();
	await page.getByRole('button', { name: 'Schedule task' }).click();
	await expect(page).toHaveURL(/\/scheduled\/[0-9a-f-]+$/);

	// A direct schedule's own conversation is promoted to a continuable chat
	// on confirm, so the "View in chat" link (Task 7) is already present.
	const viewInChat = page.getByRole('link', { name: 'View in chat →' });
	await expect(viewInChat).toBeVisible();
	const chatHref = await viewInChat.getAttribute('href');
	if (!chatHref) throw new Error('View in chat link has no href');
	expect(chatHref).toMatch(/^\/chat\/[^/]+$/);

	// 2. Trigger Run now.
	await expect(page.getByRole('button', { name: 'Run now' })).toBeEnabled();
	await page.getByRole('button', { name: 'Run now' }).click();
	const delivered = page.getByRole('heading', { name: 'Run history' }).locator('..').locator('li').first();
	await expect(delivered).toContainText('Delivered', { timeout: 30_000 });
	await expect(delivered.locator('p')).not.toHaveText('');

	// 3. Follow "View in chat" -> the result renders as an assistant message
	// in /chat with the 🔔 scheduled badge (Task 8).
	await viewInChat.click();
	await expect(page).toHaveURL(new RegExp(`${chatHref}$`));
	await expect(page.locator('.scheduled-badge')).toHaveText('🔔 Scheduled result');
	await expect(page.getByText('Time to drink some water.', { exact: true })).toBeVisible();

	// Reloading forces the Sidebar to refresh conversations; the delivery
	// conversation must now rank ahead of the earlier "Just chatting..."
	// conversation in Recents (Task 3's bump-to-top on delivery).
	await page.reload();
	const recentLinks = page.locator('#sidebar-recent-conversations .conversation-list li a');
	await expect(recentLinks.first()).toBeVisible();
	const recentHrefs = await recentLinks.evaluateAll((links) =>
		links.map((link) => new URL((link as HTMLAnchorElement).href).pathname)
	);
	const deliveryIndex = recentHrefs.indexOf(chatHref);
	const seedIndex = recentHrefs.indexOf(seedChatPath);
	expect(deliveryIndex, `delivery conversation ${chatHref} missing from Recents: ${recentHrefs.join(', ')}`).toBeGreaterThanOrEqual(0);
	expect(seedIndex, `seed conversation ${seedChatPath} missing from Recents: ${recentHrefs.join(', ')}`).toBeGreaterThanOrEqual(0);
	expect(deliveryIndex, 'delivery conversation should be bumped above the seed conversation').toBeLessThan(seedIndex);

	// 4. Reply in that chat -> a normal assistant turn streams back through
	// the ordinary chat pipeline.
	await sendChat(page, 'Thanks - any tips for staying hydrated during long runs?');
	await expect(page.getByText(/test coaching reply/i)).toBeVisible();
	await expect(page.getByRole('alert')).toHaveCount(0);
});
