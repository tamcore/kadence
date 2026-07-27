import { getPushConfig, registerPushSubscription, deletePushSubscription } from '$lib/api/push';
import { pushSupported, pushPermission, pushSubscribed } from '$lib/stores/push';

export function urlBase64ToUint8Array(base64: string): Uint8Array {
	const padding = '='.repeat((4 - (base64.length % 4)) % 4);
	const normalized = (base64 + padding).replace(/-/g, '+').replace(/_/g, '/');
	const raw = atob(normalized);
	const out = new Uint8Array(raw.length);
	for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
	return out;
}

export async function enablePush(): Promise<'subscribed' | 'denied' | 'unsupported' | 'disabled'> {
	if (!pushSupported) return 'unsupported';
	const cfg = await getPushConfig();
	if (!cfg.enabled || !cfg.vapidPublicKey) return 'disabled';

	const permission = await Notification.requestPermission();
	pushPermission.set(permission);
	if (permission !== 'granted') return 'denied';

	const reg = await navigator.serviceWorker.ready;
	const existing = await reg.pushManager.getSubscription();
	const sub =
		existing ??
		(await reg.pushManager.subscribe({
			userVisibleOnly: true,
			applicationServerKey: urlBase64ToUint8Array(cfg.vapidPublicKey) as BufferSource
		}));

	const json = sub.toJSON();
	await registerPushSubscription({
		endpoint: sub.endpoint,
		keys: { p256dh: json.keys?.p256dh ?? '', auth: json.keys?.auth ?? '' }
	});
	pushSubscribed.set(true);
	return 'subscribed';
}

export async function disablePush(): Promise<void> {
	if (!pushSupported) return;
	const reg = await navigator.serviceWorker.ready;
	const sub = await reg.pushManager.getSubscription();
	if (sub) {
		await deletePushSubscription(sub.endpoint);
		await sub.unsubscribe();
	}
	pushSubscribed.set(false);
}
