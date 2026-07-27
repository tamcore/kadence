// Real OS push delivery (browser push service, service worker registration,
// actual notification display) is out of scope for e2e — headless Chromium
// has no real push service and there is nothing meaningful to observe there.
// This spec stubs `navigator.serviceWorker.ready` / `pushManager` and asserts
// only the subscribe → POST /api/push/subscriptions wiring works end to end.

import { expect, test } from '@playwright/test';
import { login } from './helpers';

const ADMIN_USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

test('enabling notifications posts a subscription', async ({ page, context }) => {
	await context.grantPermissions(['notifications']);

	// Stub pushManager to avoid needing a real push service.
	await page.addInitScript(() => {
		// @ts-expect-error test stub of serviceWorker.ready
		navigator.serviceWorker.ready = Promise.resolve({
			pushManager: {
				getSubscription: async () => null,
				subscribe: async () => ({
					endpoint: 'https://push.example/test',
					toJSON: () => ({ keys: { p256dh: 'p', auth: 'a' } }),
					unsubscribe: async () => true
				})
			}
		});
	});

	const posted: string[] = [];
	await page.route('**/api/push/subscriptions', async (route) => {
		posted.push(route.request().postData() ?? '');
		await route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: '{"data":{"ok":true}}'
		});
	});
	await page.route('**/api/push/config', (route) =>
		route.fulfill({
			status: 200,
			contentType: 'application/json',
			body: '{"data":{"enabled":true,"vapidPublicKey":"AQID"}}'
		})
	);

	await login(page, ADMIN_USERNAME, ADMIN_PASSWORD);
	await page.goto('/profile');
	await page.getByTestId('toggle-push').click();

	await expect.poll(() => posted.length).toBeGreaterThan(0);
	expect(posted[0]).toContain('https://push.example/test');
});
