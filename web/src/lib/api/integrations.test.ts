import { describe, it, expect, vi, beforeEach } from 'vitest';
import {
	listIntegrations,
	startLink,
	unlinkIntegration,
	integrationLabel,
	type Integration
} from './integrations';
import { setCsrfToken } from './client';

function jsonResponse(status: number, body: unknown): Response {
	return new Response(status === 204 ? null : JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});
}

const linked: Integration = {
	server: 'garmin',
	linked: true,
	status: 'linked',
	scope: 'garmin:read',
	access_expires_at: '2026-08-17T21:00:00Z'
};

describe('integrations api', () => {
	beforeEach(() => {
		setCsrfToken('tok');
		vi.restoreAllMocks();
	});

	it('GETs /api/mcp/integrations and returns the list', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: [linked] }));
		vi.stubGlobal('fetch', fetchMock);

		const got = await listIntegrations();

		expect(got).toEqual([linked]);
		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toBe('/api/mcp/integrations');
		expect(init.method).toBe('GET');
	});

	it('POSTs the start endpoint with the CSRF header and returns the authorize URL', async () => {
		const fetchMock = vi
			.fn()
			.mockResolvedValue(
				jsonResponse(200, { data: { authorize_url: 'https://garmin.invalid/authorize' } })
			);
		vi.stubGlobal('fetch', fetchMock);

		const got = await startLink('garmin');

		expect(got.authorize_url).toBe('https://garmin.invalid/authorize');
		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toBe('/api/mcp/oauth/garmin/start');
		expect(init.method).toBe('POST');
		expect(init.headers['X-CSRF-Token']).toBe('tok');
	});

	it('DELETEs the integration to unlink it', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(204, null));
		vi.stubGlobal('fetch', fetchMock);

		await unlinkIntegration('garmin');

		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toBe('/api/mcp/oauth/garmin');
		expect(init.method).toBe('DELETE');
	});

	it('escapes the server id in the path', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(204, null));
		vi.stubGlobal('fetch', fetchMock);

		await unlinkIntegration('a/b');

		expect(fetchMock.mock.calls[0][0]).toBe('/api/mcp/oauth/a%2Fb');
	});

	it('labels a known integration and falls back for an unknown one', () => {
		expect(integrationLabel('garmin')).toBe('Garmin Connect');
		expect(integrationLabel('strava')).toBe('Strava');
	});
});
