import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$lib/pwa/reachability-monitor', () => ({
	reachabilityMonitor: { probeNow: vi.fn().mockResolvedValue(undefined) }
}));

import { goto } from '$app/navigation';
import { api, APIError, getCsrfToken, setCsrfToken } from './client';
import { isAuthenticated, setAuth } from '$lib/stores/auth';
import { setOnline, setServerReachable, UNREACHABLE_MESSAGE } from '$lib/stores/connection';
import { reachabilityMonitor } from '$lib/pwa/reachability-monitor';

function mockFetch(status: number, body: unknown, headers: Record<string, string> = {}) {
	return vi.fn().mockResolvedValue(
		new Response(body === undefined ? null : JSON.stringify(body), {
			status,
			headers: { 'Content-Type': 'application/json', ...headers }
		})
	);
}

afterEach(() => {
	vi.restoreAllMocks();
	vi.clearAllMocks();
	setCsrfToken(null);
	window.history.pushState({}, '', '/');
});

describe('api client', () => {
	it('login posts credentials and returns the user from the envelope', async () => {
		const f = mockFetch(200, { data: { id: 1, username: 'alice', email: 'a@x.io', role: 'user' } });
		vi.stubGlobal('fetch', f);

		const user = await api.login('alice', 'pw', true);

		expect(user.username).toBe('alice');
		const [url, opts] = f.mock.calls[0];
		expect(url).toBe('/api/session');
		expect(opts.method).toBe('POST');
		expect(opts.credentials).toBe('include');
		expect(JSON.parse(opts.body)).toEqual({ username: 'alice', password: 'pw', remember: true });
	});

	it('captures X-CSRF-Token from a response and sends it on unsafe requests', async () => {
		const getF = mockFetch(200, { data: { id: 1, username: 'a', email: 'a@x.io', role: 'admin' } }, { 'X-CSRF-Token': 'tok123' });
		vi.stubGlobal('fetch', getF);
		await api.getCurrentUser();
		expect(getCsrfToken()).toBe('tok123');

		const delF = mockFetch(200, { data: { ok: true } });
		vi.stubGlobal('fetch', delF);
		await api.logout();
		const [, opts] = delF.mock.calls[0];
		expect(opts.headers['X-CSRF-Token']).toBe('tok123');
		expect(opts.method).toBe('DELETE');
	});

	it('throws APIError with the envelope error message on non-2xx', async () => {
		vi.stubGlobal('fetch', mockFetch(401, { data: null, error: 'invalid username or password' }));
		await expect(api.login('a', 'bad', false)).rejects.toThrow(APIError);
	});
});

describe('central 401 handling', () => {
	it('clears auth and redirects to /login with a returnTo on a 401', async () => {
		window.history.pushState({}, '', '/conversations/123?foo=bar');
		setAuth({
			id: 1,
			username: 'alice',
			email: 'a@x.io',
			role: 'user',
			displayName: 'Alice',
			unitSystem: 'metric',
			location: '',
			aboutMe: '',
			timezone: 'UTC',
			scheduledEnabled: false
		});
		vi.stubGlobal('fetch', mockFetch(401, { data: null, error: 'unauthorized' }));

		await expect(api.get('/conversations')).rejects.toThrow(APIError);

		expect(get(isAuthenticated)).toBe(false);
		expect(goto).toHaveBeenCalledWith(
			'/login?returnTo=' + encodeURIComponent('/conversations/123?foo=bar')
		);
	});

	it('leaves auth/navigation untouched for non-401 errors', async () => {
		setAuth({
			id: 1,
			username: 'alice',
			email: 'a@x.io',
			role: 'user',
			displayName: 'Alice',
			unitSystem: 'metric',
			location: '',
			aboutMe: '',
			timezone: 'UTC',
			scheduledEnabled: false
		});
		vi.stubGlobal('fetch', mockFetch(500, { data: null, error: 'boom' }));

		await expect(api.get('/conversations')).rejects.toThrow(APIError);

		expect(get(isAuthenticated)).toBe(true);
		expect(goto).not.toHaveBeenCalled();
	});

	it('does not redirect again when the failing request originates from the login page', async () => {
		window.history.pushState({}, '', '/login');
		vi.stubGlobal('fetch', mockFetch(401, { data: null, error: 'invalid username or password' }));

		await expect(api.login('a', 'bad', false)).rejects.toThrow(APIError);

		expect(goto).not.toHaveBeenCalled();
	});
});

describe('request timeout', () => {
	it('rejects when the response never arrives', async () => {
		vi.useFakeTimers();
		try {
			vi.stubGlobal(
				'fetch',
				(_url: string, init?: RequestInit) =>
					new Promise((_resolve, reject) => {
						init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
					})
			);
			const pending = expect(api.get('/context/overview')).rejects.toBeTruthy();
			await vi.advanceTimersByTimeAsync(15000);
			await pending;
		} finally {
			vi.useRealTimers();
		}
	}, 5000);
});

describe('reachability guard', () => {
	beforeEach(() => {
		setOnline(true);
		setServerReachable(true);
		vi.mocked(reachabilityMonitor.probeNow).mockClear();
	});

	it('rejects without fetching when unreachable', async () => {
		const fetchSpy = vi.spyOn(globalThis, 'fetch');
		setServerReachable(false);
		await expect(api.get('/anything')).rejects.toMatchObject({
			name: 'APIError',
			message: UNREACHABLE_MESSAGE
		});
		expect(fetchSpy).not.toHaveBeenCalled();
	});

	it('triggers a probe on a network-class fetch rejection', async () => {
		vi.spyOn(globalThis, 'fetch').mockRejectedValueOnce(new TypeError('failed to fetch'));
		await expect(api.get('/anything')).rejects.toBeInstanceOf(APIError);
		expect(reachabilityMonitor.probeNow).toHaveBeenCalledTimes(1);
	});

	it('triggers a probe on a 503 response', async () => {
		vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
			new Response('{"error":"down"}', { status: 503, headers: { 'content-type': 'application/json' } })
		);
		await expect(api.get('/anything')).rejects.toBeInstanceOf(APIError);
		expect(reachabilityMonitor.probeNow).toHaveBeenCalledTimes(1);
	});

	it('does NOT probe on a 400 response', async () => {
		vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
			new Response('{"error":"bad"}', { status: 400, headers: { 'content-type': 'application/json' } })
		);
		await expect(api.get('/anything')).rejects.toBeInstanceOf(APIError);
		expect(reachabilityMonitor.probeNow).not.toHaveBeenCalled();
	});
});
