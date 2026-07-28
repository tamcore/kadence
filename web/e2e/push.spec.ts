// Real push subscription + delivery needs a browser push service and an
// assignable `navigator.serviceWorker.ready`, neither available in headless
// Chromium — so the subscribe→deliver path is covered by the internal/push and
// web unit tests, not here. This e2e asserts the deterministic, live-observable
// part: with the server reporting push enabled (KADENCE_PUSH_VAPID_* set in
// scripts/e2e-web.sh) and the browser exposing the web-push APIs, the profile
// "enable notifications" toggle renders — /api/push/config, the store, and the
// gating are all wired end to end against the real app. Where the headless
// browser lacks the web-push APIs (so `pushSupported` is false and the toggle
// is intentionally hidden), the test skips rather than fails.

import { expect, test } from '@playwright/test';
import { login } from './helpers';

const ADMIN_USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

test('profile shows the enable-notifications toggle when push is supported', async ({ page, context }) => {
	await context.grantPermissions(['notifications']);

	await login(page, ADMIN_USERNAME, ADMIN_PASSWORD);
	await page.goto('/profile');

	// Mirror the app's own `pushSupported` capability check. If this headless
	// browser doesn't expose the web-push APIs, the toggle is correctly hidden —
	// skip rather than report a false failure.
	const pushSupported = await page.evaluate(
		() => 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window
	);
	test.skip(!pushSupported, 'headless browser lacks web push APIs; toggle is intentionally hidden');

	await expect(page.getByTestId('toggle-push')).toBeVisible();
});
