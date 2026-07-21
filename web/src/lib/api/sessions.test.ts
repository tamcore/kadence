import { describe, it, expect, vi, beforeEach } from 'vitest';
import { listSessions, revokeSession, revokeOtherSessions } from './sessions';
import { setCsrfToken, APIError } from './client';

function jsonResponse(status: number, body: unknown): Response {
	return new Response(status === 204 ? null : JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});
}

const sampleSessions = [
	{
		publicId: 'pub-1',
		device: 'Chrome on macOS',
		ip: '203.0.113.10',
		createdAt: '2026-07-01T12:00:00Z',
		lastSeenAt: '2026-07-21T09:00:00Z',
		current: true
	},
	{
		publicId: 'pub-2',
		device: 'Safari on iOS',
		ip: '203.0.113.20',
		createdAt: '2026-06-15T08:00:00Z',
		lastSeenAt: '2026-07-20T18:30:00Z',
		current: false
	}
];

describe('sessions api', () => {
	beforeEach(() => {
		setCsrfToken('tok');
		vi.restoreAllMocks();
	});

	it('GETs /api/sessions and returns the session list', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: sampleSessions }));
		vi.stubGlobal('fetch', fetchMock);

		const sessions = await listSessions();

		expect(sessions).toEqual(sampleSessions);
		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toBe('/api/sessions');
		expect(init.method).toBe('GET');
		expect(init.credentials).toBe('include');
	});

	it('DELETEs /api/sessions/{publicId}', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: null }));
		vi.stubGlobal('fetch', fetchMock);

		await revokeSession('pub-2');

		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toBe('/api/sessions/pub-2');
		expect(init.method).toBe('DELETE');
		expect(init.headers['X-CSRF-Token']).toBe('tok');
	});

	it('encodes the publicId when DELETEing a session', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: null }));
		vi.stubGlobal('fetch', fetchMock);

		await revokeSession('pub/with space');

		const [url] = fetchMock.mock.calls[0];
		expect(url).toBe(`/api/sessions/${encodeURIComponent('pub/with space')}`);
	});

	it('POSTs /api/sessions/revoke-others with an empty body', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: null }));
		vi.stubGlobal('fetch', fetchMock);

		await revokeOtherSessions();

		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toBe('/api/sessions/revoke-others');
		expect(init.method).toBe('POST');
		expect(JSON.parse(init.body)).toEqual({});
	});

	it('throws APIError with the message when revoke-others fails without a session cookie (400)', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn().mockResolvedValue(jsonResponse(400, { error: 'no active session' }))
		);
		await expect(revokeOtherSessions()).rejects.toMatchObject({
			status: 400,
			message: 'no active session'
		});
		expect(APIError).toBeDefined();
	});
});
