import { describe, it, expect, vi, beforeEach } from 'vitest';
import { isWebAuthnEnabled, listPasskeys, renamePasskey, deletePasskey } from './webauthn';
import { setCsrfToken } from './client';

function jsonResponse(status: number, body: unknown): Response {
	return new Response(status === 204 ? null : JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});
}

describe('webauthn api', () => {
	beforeEach(() => {
		setCsrfToken('tok');
		vi.restoreAllMocks();
	});

	it('isWebAuthnEnabled returns the flag', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { data: { enabled: true } })));
		expect(await isWebAuthnEnabled()).toBe(true);
	});

	it('isWebAuthnEnabled returns false on error', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(500, { error: 'boom' })));
		expect(await isWebAuthnEnabled()).toBe(false);
	});

	it('lists passkeys', async () => {
		const list = [{ publicId: 'p1', name: 'MacBook', createdAt: '2026-07-01T00:00:00Z', lastUsedAt: null }];
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { data: list })));
		expect(await listPasskeys()).toEqual(list);
	});

	it('PATCHes rename with encoded id', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: { ok: true } }));
		vi.stubGlobal('fetch', fetchMock);
		await renamePasskey('p/1', 'New');
		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toBe(`/api/webauthn/credentials/${encodeURIComponent('p/1')}`);
		expect(init.method).toBe('PATCH');
		expect(JSON.parse(init.body)).toEqual({ name: 'New' });
	});

	it('DELETEs a passkey', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: { ok: true } }));
		vi.stubGlobal('fetch', fetchMock);
		await deletePasskey('p2');
		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toBe('/api/webauthn/credentials/p2');
		expect(init.method).toBe('DELETE');
	});
});
