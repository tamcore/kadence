import { describe, expect, it, vi } from 'vitest';
import { PwaLifecycle, type PwaStatus } from './lifecycle';

type Listener = () => void;

function eventTarget() {
	const listeners = new Map<string, Set<Listener>>();
	return {
		addEventListener(type: string, listener: Listener) {
			const current = listeners.get(type) ?? new Set<Listener>();
			current.add(listener);
			listeners.set(type, current);
		},
		removeEventListener(type: string, listener: Listener) {
			listeners.get(type)?.delete(listener);
		},
		dispatch(type: string) {
			for (const listener of listeners.get(type) ?? []) listener();
		},
		count(type: string) {
			return listeners.get(type)?.size ?? 0;
		}
	};
}

function setup(options: { online?: boolean; waiting?: boolean } = {}) {
	const windowEvents = eventTarget();
	const containerEvents = eventTarget();
	const registrationEvents = eventTarget();
	const waitingWorker = { postMessage: vi.fn() };
	const registration = {
		waiting: options.waiting ? waitingWorker : null,
		installing: null,
		update: vi.fn(async () => {}),
		addEventListener: registrationEvents.addEventListener,
		removeEventListener: registrationEvents.removeEventListener
	};
	const serviceWorker = {
		controller: {},
		getRegistration: vi.fn(async () => registration),
		addEventListener: containerEvents.addEventListener,
		removeEventListener: containerEvents.removeEventListener
	};
	const navigatorRef = {
		onLine: options.online ?? true,
		serviceWorker
	};
	const reload = vi.fn();
	const windowRef = {
		addEventListener: windowEvents.addEventListener,
		removeEventListener: windowEvents.removeEventListener,
		location: { reload }
	};
	const statuses: PwaStatus[] = [];
	const lifecycle = new PwaLifecycle((status) => statuses.push(status), navigatorRef, windowRef);

	return {
		lifecycle,
		statuses,
		registration,
		waitingWorker,
		navigatorRef,
		windowEvents,
		containerEvents,
		registrationEvents,
		reload
	};
}

describe('PwaLifecycle', () => {
	it('reports initial connectivity and an already-waiting update', async () => {
		const { lifecycle, statuses } = setup({ online: false, waiting: true });

		await lifecycle.start();

		expect(statuses.at(-1)).toEqual({
			online: false,
			updateAvailable: true,
			applyingUpdate: false
		});
	});

	it('checks for a new worker and exposes it when waiting', async () => {
		const { lifecycle, statuses, registration, waitingWorker } = setup();
		await lifecycle.start();
		registration.update.mockImplementationOnce(async () => {
			registration.waiting = waitingWorker;
		});

		await lifecycle.checkForUpdate();

		expect(registration.update).toHaveBeenCalledOnce();
		expect(statuses.at(-1)?.updateAvailable).toBe(true);
	});

	it('activates only after request and reloads once when control changes', async () => {
		const { lifecycle, waitingWorker, containerEvents, reload, statuses } = setup({
			waiting: true
		});
		await lifecycle.start();

		lifecycle.applyUpdate();
		containerEvents.dispatch('controllerchange');
		containerEvents.dispatch('controllerchange');

		expect(waitingWorker.postMessage).toHaveBeenCalledWith({ type: 'SKIP_WAITING' });
		expect(statuses.at(-1)?.applyingUpdate).toBe(true);
		expect(reload).toHaveBeenCalledOnce();
	});

	it('tracks online changes and removes listeners on destroy', async () => {
		const { lifecycle, statuses, navigatorRef, windowEvents, containerEvents } = setup();
		await lifecycle.start();
		navigatorRef.onLine = false;

		windowEvents.dispatch('offline');
		expect(statuses.at(-1)?.online).toBe(false);

		lifecycle.destroy();
		expect(windowEvents.count('online')).toBe(0);
		expect(windowEvents.count('offline')).toBe(0);
		expect(containerEvents.count('controllerchange')).toBe(0);
	});
});
