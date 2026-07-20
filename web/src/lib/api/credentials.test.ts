import { describe, it, expect, vi, beforeEach } from 'vitest';
import { submitCredentials } from './credentials';
import { setCsrfToken, APIError } from './client';

function jsonResponse(status: number, body: unknown): Response {
	return new Response(status === 204 ? null : JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});
}

describe('credentials api', () => {
	beforeEach(() => {
		setCsrfToken('tok');
		vi.restoreAllMocks();
	});

	it('POSTs values to /api/credentials/{requestId} with CSRF + credentials', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: null }));
		vi.stubGlobal('fetch', fetchMock);

		await submitCredentials('req-1', { api_key: 'secret-value' });

		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toBe('/api/credentials/req-1');
		expect(init.method).toBe('POST');
		expect(init.credentials).toBe('include');
		expect(init.headers['X-CSRF-Token']).toBe('tok');
		expect(JSON.parse(init.body)).toEqual({ values: { api_key: 'secret-value' } });
	});

	it('throws APIError on failure', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(400, { error: 'bad request' })));
		await expect(submitCredentials('req-1', { x: 'y' })).rejects.toBeInstanceOf(APIError);
	});
});
