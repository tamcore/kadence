import { describe, it, expect, vi, beforeEach } from 'vitest';
import * as client from '$lib/api/client';
import { getPushConfig, registerPushSubscription, deletePushSubscription } from './push';

describe('push api', () => {
	beforeEach(() => vi.restoreAllMocks());

	it('getPushConfig calls /push/config', async () => {
		const spy = vi.spyOn(client.api, 'get').mockResolvedValue({ enabled: true, vapidPublicKey: 'k' });
		const cfg = await getPushConfig();
		expect(spy).toHaveBeenCalledWith('/push/config');
		expect(cfg.vapidPublicKey).toBe('k');
	});

	it('registerPushSubscription posts to /push/subscriptions', async () => {
		const spy = vi.spyOn(client.api, 'post').mockResolvedValue({ ok: true });
		await registerPushSubscription({ endpoint: 'e', keys: { p256dh: 'p', auth: 'a' } });
		expect(spy).toHaveBeenCalledWith('/push/subscriptions', { endpoint: 'e', keys: { p256dh: 'p', auth: 'a' } });
	});

	it('deletePushSubscription sends endpoint in body', async () => {
		const spy = vi.spyOn(client.api, 'delWithBody').mockResolvedValue({ ok: true });
		await deletePushSubscription('e');
		expect(spy).toHaveBeenCalledWith('/push/subscriptions', { endpoint: 'e' });
	});
});
