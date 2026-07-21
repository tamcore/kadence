import { get } from 'svelte/store';
import { beforeEach, describe, expect, it } from 'vitest';
import { clearAuth, currentUser, isAuthenticated, setAuth } from './auth';

beforeEach(() => {
	localStorage.clear();
	clearAuth();
});

describe('auth store', () => {
	it('setAuth stores the user and flips isAuthenticated', () => {
		setAuth({
			id: 1,
			username: 'alice',
			email: 'a@x.io',
			role: 'admin',
			displayName: 'Alice',
			unitSystem: 'metric'
		});
		expect(get(currentUser)?.username).toBe('alice');
		expect(get(isAuthenticated)).toBe(true);
		expect(localStorage.getItem('kadence_user')).toContain('alice');
	});

	it('clearAuth resets state', () => {
		setAuth({
			id: 1,
			username: 'alice',
			email: 'a@x.io',
			role: 'user',
			displayName: 'Alice',
			unitSystem: 'metric'
		});
		clearAuth();
		expect(get(currentUser)).toBeNull();
		expect(get(isAuthenticated)).toBe(false);
	});
});
