/// <reference no-default-lib="true"/>
/// <reference lib="esnext" />
/// <reference lib="webworker" />
/// <reference types="@sveltejs/kit" />

import { build, files, version } from '$service-worker';
import { classifyRequest } from '$lib/pwa/cache-policy';
import { resolveClientToFocus } from '$lib/push/notification-target';

const worker = globalThis as unknown as ServiceWorkerGlobalScope;
const CACHE_PREFIX = 'kadence-shell-';
const CACHE = `${CACHE_PREFIX}${version}`;
const SHELL = '/';
const PRECACHE = [...new Set([SHELL, ...build, ...files])];
const PRECACHED_PATHS = new Set(
	PRECACHE.map((path) => new URL(path, worker.location.origin).pathname)
);

worker.addEventListener('install', (event) => {
	event.waitUntil(caches.open(CACHE).then((cache) => cache.addAll(PRECACHE)));
});

worker.addEventListener('activate', (event) => {
	event.waitUntil(
		Promise.all([
			caches
				.keys()
				.then((keys) =>
					Promise.all(
						keys
							.filter((key) => key.startsWith(CACHE_PREFIX) && key !== CACHE)
							.map((key) => caches.delete(key))
					)
				),
			worker.clients.claim()
		])
	);
});

worker.addEventListener('message', (event) => {
	if (event.data?.type === 'SKIP_WAITING') {
		void worker.skipWaiting();
	}
});

interface PushPayload {
	title?: string;
	body?: string;
	url?: string;
	tag?: string;
}

worker.addEventListener('push', (event: PushEvent) => {
	if (!event.data) return;
	let payload: PushPayload;
	try {
		payload = event.data.json();
	} catch {
		payload = { title: 'Kadence', body: event.data.text() };
	}
	event.waitUntil(
		worker.registration.showNotification(payload.title ?? 'Kadence', {
			body: payload.body ?? '',
			icon: '/icons/icon-192.png',
			badge: '/icons/icon-192.png',
			tag: payload.tag,
			data: { url: payload.url ?? '/' }
		})
	);
});

worker.addEventListener('notificationclick', (event: NotificationEvent) => {
	event.notification.close();
	const targetURL: string = event.notification.data?.url ?? '/';
	event.waitUntil(
		(async () => {
			const all = await worker.clients.matchAll({ type: 'window', includeUncontrolled: true });
			const targetPath = targetURL.split('#')[0];
			const matchIndex = resolveClientToFocus(all, targetPath);
			if (matchIndex !== -1) {
				const client = all[matchIndex] as WindowClient;
				await client.focus();
				client.postMessage({ type: 'NAVIGATE', url: targetURL });
				return;
			}
			await worker.clients.openWindow(targetURL);
		})()
	);
});

worker.addEventListener('fetch', (event) => {
	const strategy = classifyRequest(event.request, worker.location.origin, PRECACHED_PATHS);
	if (strategy === 'ignore' || strategy === 'network') return;

	if (strategy === 'precache') {
		event.respondWith(
			caches
				.open(CACHE)
				.then(
					async (cache) =>
						(await cache.match(new URL(event.request.url).pathname)) ?? fetch(event.request)
				)
		);
		return;
	}

	event.respondWith(
		fetch(event.request).catch(async (error: unknown) => {
			const cachedShell = await caches.open(CACHE).then((cache) => cache.match(SHELL));
			if (cachedShell) return cachedShell;
			throw error;
		})
	);
});
