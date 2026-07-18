import { derived, writable } from 'svelte/store';
import type { User } from '$lib/types';

function persisted<T>(key: string, initial: T) {
	let start: string | null = null;
	try {
		start = typeof localStorage !== 'undefined' ? localStorage.getItem(key) : null;
	} catch {
		start = null;
	}
	const store = writable<T>(start ? (JSON.parse(start) as T) : initial);
	try {
		if (typeof localStorage !== 'undefined') {
			store.subscribe((v) => {
				if (v === null) localStorage.removeItem(key);
				else localStorage.setItem(key, JSON.stringify(v));
			});
		}
	} catch {
		// localStorage not available, store is in-memory only
	}
	return store;
}

export const currentUser = persisted<User | null>('kadence_user', null);
export const isAuthenticated = persisted<boolean>('kadence_authed', false);

export const isAdmin = derived(currentUser, ($u) => $u?.role === 'admin');

export function setAuth(user: User): void {
	currentUser.set(user);
	isAuthenticated.set(true);
}

export function clearAuth(): void {
	currentUser.set(null);
	isAuthenticated.set(false);
}
