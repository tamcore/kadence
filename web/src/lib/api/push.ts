import { api } from '$lib/api/client';

export interface PushConfig {
	enabled: boolean;
	vapidPublicKey: string;
}

export interface PushSubscriptionInput {
	endpoint: string;
	keys: { p256dh: string; auth: string };
}

export const getPushConfig = () => api.get<PushConfig>('/push/config');

export const registerPushSubscription = (body: PushSubscriptionInput) =>
	api.post<{ ok: boolean }>('/push/subscriptions', body);

export const deletePushSubscription = (endpoint: string) =>
	api.delWithBody<{ ok: boolean }>('/push/subscriptions', { endpoint });
