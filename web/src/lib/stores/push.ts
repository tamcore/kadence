import { writable } from 'svelte/store';
import { getPushConfig } from '$lib/api/push';

export const pushSupported =
	typeof navigator !== 'undefined' &&
	'serviceWorker' in navigator &&
	typeof window !== 'undefined' &&
	'PushManager' in window &&
	'Notification' in window;

export const pushPermission = writable<NotificationPermission>('default');
export const pushSubscribed = writable(false);
export const pushServerEnabled = writable(false);

export async function refreshPushState(): Promise<void> {
	if (!pushSupported) return;
	pushPermission.set(Notification.permission);
	try {
		const cfg = await getPushConfig();
		pushServerEnabled.set(cfg.enabled);
	} catch {
		pushServerEnabled.set(false);
	}
	try {
		const reg = await navigator.serviceWorker.ready;
		const sub = await reg.pushManager.getSubscription();
		pushSubscribed.set(sub !== null);
	} catch {
		pushSubscribed.set(false);
	}
}
