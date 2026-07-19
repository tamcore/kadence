import { describe, it, expect, vi, beforeEach } from 'vitest';
import { uploadDocument, listDocuments, deleteDocument } from './documents';
import { setCsrfToken, APIError } from './client';

function jsonResponse(status: number, body: unknown): Response {
	return new Response(status === 204 ? null : JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});
}

const sampleDoc = {
	id: 1, filename: 'p.pdf', mime: 'application/pdf',
	source_type: 'pdf', scope: 'private', created_at: '2026-07-19T10:00:00Z'
};

describe('documents api', () => {
	beforeEach(() => {
		setCsrfToken('tok');
		vi.restoreAllMocks();
	});

	it('uploads a file as multipart with the CSRF header and no JSON content-type', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: sampleDoc }));
		vi.stubGlobal('fetch', fetchMock);

		const file = new File([new Uint8Array([1, 2, 3])], 'p.pdf', { type: 'application/pdf' });
		const doc = await uploadDocument(file);

		expect(doc.id).toBe(1);
		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toBe('/api/documents');
		expect(init.method).toBe('POST');
		expect(init.body).toBeInstanceOf(FormData);
		expect((init.body as FormData).get('file')).toBeInstanceOf(File);
		expect(init.credentials).toBe('include');
		expect(init.headers['X-CSRF-Token']).toBe('tok');
		// browser must set the multipart boundary itself
		expect(init.headers['Content-Type']).toBeUndefined();
	});

	it('uploads to the admin endpoint when admin: true', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: { ...sampleDoc, scope: 'public' } }));
		vi.stubGlobal('fetch', fetchMock);
		await uploadDocument(new File(['x'], 'p.pdf', { type: 'application/pdf' }), { admin: true });
		expect(fetchMock.mock.calls[0][0]).toBe('/api/admin/documents');
	});

	it('throws APIError(415) for an unsupported type', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(415, { error: 'unsupported' })));
		await expect(uploadDocument(new File(['x'], 'x.png', { type: 'image/png' }))).rejects.toMatchObject({ status: 415 });
	});

	it('throws APIError(413) when too large', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(413, { error: 'too large' })));
		await expect(uploadDocument(new File(['x'], 'p.pdf', { type: 'application/pdf' }))).rejects.toBeInstanceOf(APIError);
	});

	it('lists and deletes via the shared client (user + admin paths)', async () => {
		const fetchMock = vi.fn()
			.mockResolvedValueOnce(jsonResponse(200, { data: [sampleDoc] }))
			.mockResolvedValueOnce(jsonResponse(204, null))
			.mockResolvedValueOnce(jsonResponse(200, { data: [] }));
		vi.stubGlobal('fetch', fetchMock);

		const list = await listDocuments();
		expect(list).toHaveLength(1);
		expect(fetchMock.mock.calls[0][0]).toBe('/api/documents');

		await deleteDocument(1);
		expect(fetchMock.mock.calls[1][0]).toBe('/api/documents/1');

		await listDocuments({ admin: true });
		expect(fetchMock.mock.calls[2][0]).toBe('/api/admin/documents');
	});
});
