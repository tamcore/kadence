import { afterEach, describe, expect, it, vi } from 'vitest';
import { api, APIError, getCsrfToken, setCsrfToken } from './client';

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
	setCsrfToken(null);
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
