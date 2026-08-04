import { writable, derived, get, type Readable, type Writable } from 'svelte/store';

export const UNREACHABLE_MESSAGE = 'Server unreachable. Check your connection and try again.';

export const online: Writable<boolean> = writable(true);
export const serverReachable: Writable<boolean> = writable(true);

export const canReachServer: Readable<boolean> = derived(
	[online, serverReachable],
	([$online, $serverReachable]) => $online && $serverReachable
);

export function setOnline(value: boolean): void {
	online.set(value);
}

export function setServerReachable(value: boolean): void {
	serverReachable.set(value);
}

export function canReachServerNow(): boolean {
	return get(online) && get(serverReachable);
}
