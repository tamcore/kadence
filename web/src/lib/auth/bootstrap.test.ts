import { describe, expect, it, vi } from 'vitest';
import type { User } from '$lib/types';
import { APIError } from '$lib/api/client';
import { bootstrapSession } from './bootstrap';

const user: User = {
	id: 1,
	username: 'alice',
	email: 'alice@example.com',
	role: 'user',
	displayName: 'Alice',
	unitSystem: 'metric',
	location: '',
	aboutMe: '',
	timezone: 'UTC',
	scheduledEnabled: false
};

describe('bootstrapSession', () => {
	it('stores a confirmed session', async () => {
		const setAuth = vi.fn();
		const clearAuth = vi.fn();

		const result = await bootstrapSession(async () => user, { setAuth, clearAuth });

		expect(result).toBe('authenticated');
		expect(setAuth).toHaveBeenCalledWith(user);
		expect(clearAuth).not.toHaveBeenCalled();
	});

	it('clears local auth only after a confirmed unauthorized response', async () => {
		const setAuth = vi.fn();
		const clearAuth = vi.fn();

		const result = await bootstrapSession(
			async () => {
				throw new APIError(401, 'unauthorized');
			},
			{ setAuth, clearAuth }
		);

		expect(result).toBe('unauthorized');
		expect(clearAuth).toHaveBeenCalledOnce();
		expect(setAuth).not.toHaveBeenCalled();
	});

	it.each([
		['network failure', new TypeError('Failed to fetch')],
		['server failure', new APIError(503, 'unavailable')]
	])('retains last-known auth after %s', async (_name, failure) => {
		const setAuth = vi.fn();
		const clearAuth = vi.fn();

		const result = await bootstrapSession(
			async () => {
				throw failure;
			},
			{ setAuth, clearAuth }
		);

		expect(result).toBe('unavailable');
		expect(clearAuth).not.toHaveBeenCalled();
		expect(setAuth).not.toHaveBeenCalled();
	});
});
