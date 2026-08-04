import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$lib/pwa/reachability-monitor', () => ({
	reachabilityMonitor: { probeNow: vi.fn().mockResolvedValue(undefined) }
}));

import { submitCredentials } from './credentials';
import { setCsrfToken, APIError } from './client';
import { setOnline, setServerReachable, UNREACHABLE_MESSAGE } from '$lib/stores/connection';
import { reachabilityMonitor } from '$lib/pwa/reachability-monitor';

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
		setOnline(true);
		setServerReachable(true);
		vi.mocked(reachabilityMonitor.probeNow).mockClear();
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

	it('rejects with the unreachable message without fetching when the server is known unreachable', async () => {
		const fetchSpy = vi.fn();
		vi.stubGlobal('fetch', fetchSpy);
		setServerReachable(false);

		await expect(submitCredentials('req-1', { x: 'y' })).rejects.toMatchObject({
			name: 'APIError',
			message: UNREACHABLE_MESSAGE
		});
		expect(fetchSpy).not.toHaveBeenCalled();
	});

	it('triggers a reachability probe on a network-level fetch rejection', async () => {
		vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('failed to fetch')));

		await expect(submitCredentials('req-1', { x: 'y' })).rejects.toBeInstanceOf(APIError);
		expect(reachabilityMonitor.probeNow).toHaveBeenCalledTimes(1);
	});
});
