import { expect, test, type Locator, type Page } from '@playwright/test';
import { login } from './helpers';

const USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

async function sendChat(page: Page, message: string): Promise<void> {
	const composer = page.getByRole('textbox', { name: 'Message', exact: true });
	await composer.fill(message);
	await composer.press('Enter');
}

function taskLink(card: Locator): Locator {
	return card.getByRole('link', { name: 'Scheduled details' });
}

async function hrefs(cards: Locator): Promise<string[]> {
	return Promise.all(
		(await taskLink(cards).all()).map(async (link) => {
			const href = await link.getAttribute('href');
			if (!href) throw new Error('Scheduled card has no details link');
			return href;
		})
	);
}

async function scheduledTaskIDs(page: Page): Promise<string[]> {
	const response = await page.request.get('/api/scheduled/tasks');
	if (!response.ok()) {
		throw new Error(`Could not list Scheduled tasks before regeneration: HTTP ${response.status()}`);
	}
	const payload = (await response.json()).data as { tasks: { id: string }[] };
	return payload.tasks.map((task) => task.id).sort();
}

test('explicitly schedules, confirms, revises, preserves, and runs two weather checks', async ({ page }) => {
	test.setTimeout(90_000);
	await login(page, USERNAME, PASSWORD);
	await page.goto('/chat');

	await sendChat(page, 'What is the weather forecast for my race?');
	await expect(page.getByText(/two future weather checks/i)).toBeVisible();
	const cards = page.locator('.scheduled-artifact');
	await expect(cards).toHaveCount(0);

	await sendChat(page, 'Please schedule it as suggested');
	await expect(page.getByText('I prepared two weather checks for review.')).toBeVisible();
	const sourceURL = page.url();
	await expect(cards).toHaveCount(2);
	await expect(cards.nth(0)).toContainText('Pre-race weather check');
	await expect(cards.nth(1)).toContainText('Race-day weather check');
	for (const card of [cards.nth(0), cards.nth(1)]) {
		await expect(card).toContainText('Browser Browser Navigate, Browser Browser Snapshot');
		await expect(card).toContainText(/fetch fresh race weather/i);
	}

	await cards.nth(0).getByRole('button', { name: 'Schedule task' }).click();
	await expect(cards.nth(0)).toContainText('Scheduled');
	await expect(cards.nth(1)).toContainText('Ready to schedule');

	await page.reload();
	await expect(cards).toHaveCount(2);
	await expect(cards.nth(0)).toContainText('Scheduled');
	await expect(cards.nth(1)).toContainText('Ready to schedule');

	await cards.nth(1).getByRole('button', { name: 'Adjust' }).click();
	await page.getByLabel('Adjust scheduled task').fill('Keep the same check and focus on wind.');
	await page.getByRole('button', { name: 'Save adjustment' }).click();
	await expect(cards.nth(1)).toContainText('Race-day weather check');
	await cards.nth(1).getByRole('button', { name: 'Schedule task' }).click();
	await expect(cards.nth(1)).toContainText('Scheduled');
	const originalLinks = await hrefs(cards);
	expect(new Set(originalLinks).size).toBe(2);
	const taskIDsBeforeRegeneration = await scheduledTaskIDs(page);

	// Regenerating the source answer must reuse its relational handoff slots,
	// rather than creating a second pair of task cards or task IDs.
	await page.getByRole('button', { name: 'Regenerate response' }).last().click();
	await expect(page.getByText('I prepared two weather checks for review.')).toBeVisible();
	await expect(cards).toHaveCount(2);
	const links = await hrefs(cards);
	expect(links).toEqual(originalLinks);
	const taskIDsAfterRegeneration = await scheduledTaskIDs(page);
	expect(
		taskIDsAfterRegeneration,
		`regeneration changed Scheduled task IDs; before=${taskIDsBeforeRegeneration.join(',')} after=${taskIDsAfterRegeneration.join(',')}`
	).toEqual(taskIDsBeforeRegeneration);
	for (const link of originalLinks) {
		const id = link.split('/').at(-1);
		expect(taskIDsAfterRegeneration.filter((taskID) => taskID === id)).toHaveLength(1);
	}

	// The task detail view is the Scheduled conversation's result inbox. Run
	// each confirmed task through its normal UI path and wait for delivery.
	for (const link of links) {
		await page.goto(link);
		await expect(page.getByRole('button', { name: 'Run now' })).toBeEnabled();
		await page.getByRole('button', { name: 'Run now' }).click();
		const delivered = page.locator('.history li').first();
		await expect(delivered).toContainText('Delivered', { timeout: 30_000 });
		await expect(delivered.locator('p')).not.toHaveText('');
	}

	await page.goto(sourceURL);
	await expect(cards).toHaveCount(2);
	for (const card of [cards.nth(0), cards.nth(1)]) {
		await expect(card).toContainText('Scheduled');
		await expect(taskLink(card)).toHaveAttribute('href', /\/scheduled\/[0-9a-f-]+$/);
	}
});
