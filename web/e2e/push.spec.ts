// The web-push UI (profile toggle) and subscribe→deliver flow depend on real
// browser push APIs (Notification permission grant, an assignable
// serviceWorker.ready, a live push service) that headless Chromium does not
// provide, so those are covered by the internal/push + web unit tests and were
// verified in a real browser against the dev deployment. What this e2e asserts
// is the deterministic, live server contract the client relies on: an
// authenticated GET /api/push/config reports push enabled and returns a VAPID
// public key (and never the private key). KADENCE_PUSH_VAPID_* is set for the
// e2e app in scripts/e2e-web.sh.

import { expect, test } from '@playwright/test';
import { login } from './helpers';

const ADMIN_USERNAME = process.env.E2E_ADMIN_USERNAME || 'admin';
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'e2e-admin-pw';

test('push config endpoint reports enabled with a VAPID public key (and no private key)', async ({
	page
}) => {
	await login(page, ADMIN_USERNAME, ADMIN_PASSWORD);

	const res = await page.evaluate(async () => {
		const r = await fetch('/api/push/config', { credentials: 'include' });
		return { status: r.status, text: await r.text() };
	});

	expect(res.status).toBe(200);
	const body = JSON.parse(res.text);
	expect(body.data.enabled).toBe(true);
	expect(typeof body.data.vapidPublicKey).toBe('string');
	expect(body.data.vapidPublicKey.length).toBeGreaterThan(0);
	// The private key must never be exposed to the client.
	expect(res.text.toLowerCase()).not.toContain('private');
});
