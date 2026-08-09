import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));

import { goto } from '$app/navigation';
import {
	uploadDocument,
	listDocuments,
	deleteDocument,
	getDocumentUploadCapabilities,
	listDocumentReferences
} from './documents';
import { getCsrfToken, setCsrfToken, APIError } from './client';

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

class MockXMLHttpRequest {
	static instances: MockXMLHttpRequest[] = [];
	readonly upload: { onload: (() => void) | null } = { onload: null };
	onload: (() => void) | null = null;
	onerror: (() => void) | null = null;
	onabort: (() => void) | null = null;
	ontimeout: (() => void) | null = null;
	method = '';
	url = '';
	withCredentials = false;
	requestHeaders = new Map<string, string>();
	body: Document | FormData | null = null;
	status = 0;
	responseText = '';
	private responseHeaders = new Map<string, string>();

	constructor() {
		MockXMLHttpRequest.instances.push(this);
	}

	open(method: string, url: string): void {
		this.method = method;
		this.url = url;
	}

	setRequestHeader(name: string, value: string): void {
		this.requestHeaders.set(name, value);
	}

	getResponseHeader(name: string): string | null {
		return this.responseHeaders.get(name.toLowerCase()) ?? null;
	}

	send(body: Document | FormData): void {
		this.body = body;
	}

	respond(status: number, body: unknown, headers: Record<string, string> = {}): void {
		this.respondRaw(status, JSON.stringify(body), headers);
	}

	respondRaw(status: number, body: string, headers: Record<string, string> = {}): void {
		this.status = status;
		this.responseText = body;
		this.responseHeaders = new Map(Object.entries(headers).map(([key, value]) => [key.toLowerCase(), value]));
		this.onload?.();
	}

	fail(): void {
		this.onerror?.();
	}

	abort(): void {
		this.onabort?.();
	}

	timeout(): void {
		this.ontimeout?.();
	}
}

describe('documents api', () => {
	beforeEach(() => {
		setCsrfToken('tok');
		vi.restoreAllMocks();
		MockXMLHttpRequest.instances = [];
		vi.stubGlobal('XMLHttpRequest', MockXMLHttpRequest);
	});
	afterEach(() => vi.unstubAllGlobals());

	it('uploads through XHR and reports body completion before its response', async () => {
		const states: string[] = [];
		const file = new File([new Uint8Array([1, 2, 3])], 'p.pdf', { type: 'application/pdf' });
		const upload = uploadDocument(file, { onUploadComplete: () => states.push('processing') });
		const xhr = MockXMLHttpRequest.instances[0];

		expect(xhr.method).toBe('POST');
		expect(xhr.url).toBe('/api/documents');
		expect(xhr.withCredentials).toBe(true);
		expect(xhr.body).toBeInstanceOf(FormData);
		expect((xhr.body as FormData).get('file')).toBe(file);
		expect(xhr.requestHeaders.get('X-CSRF-Token')).toBe('tok');
		expect(xhr.requestHeaders.has('Content-Type')).toBe(false);
		xhr.upload.onload?.();
		expect(states).toEqual(['processing']);
		xhr.respond(200, { data: sampleDoc }, { 'X-CSRF-Token': 'rotated' });
		const doc = await upload;
		expect(doc.id).toBe(1);
		expect(getCsrfToken()).toBe('rotated');
	});

	it('uploads to the admin endpoint when admin: true', async () => {
		const upload = uploadDocument(new File(['x'], 'p.pdf', { type: 'application/pdf' }), { admin: true });
		const xhr = MockXMLHttpRequest.instances[0];
		expect(xhr.url).toBe('/api/admin/documents');
		xhr.respond(200, { data: { ...sampleDoc, scope: 'public' } });
		await upload;
	});

	it('throws APIError(415) for an unsupported type', async () => {
		const upload = uploadDocument(new File(['x'], 'x.png', { type: 'image/png' }));
		MockXMLHttpRequest.instances[0].respond(415, { error: 'unsupported' });
		await expect(upload).rejects.toMatchObject({ status: 415 });
	});

	it('throws APIError(413) when too large', async () => {
		const upload = uploadDocument(new File(['x'], 'p.pdf', { type: 'application/pdf' }));
		MockXMLHttpRequest.instances[0].respond(413, { error: 'too large' });
		await expect(upload).rejects.toMatchObject({ status: 413, message: 'too large' });
	});

	it('handles a 401 response and rejects with its API error', async () => {
		const upload = uploadDocument(new File(['x'], 'p.pdf', { type: 'application/pdf' }));
		MockXMLHttpRequest.instances[0].respond(401, { error: 'expired' });
		await expect(upload).rejects.toMatchObject({ status: 401, message: 'expired' });
		expect(goto).toHaveBeenCalledWith('/login?returnTo=' + encodeURIComponent('/'));
	});

	it('rejects a network error', async () => {
		const upload = uploadDocument(new File(['x'], 'p.pdf', { type: 'application/pdf' }));
		MockXMLHttpRequest.instances[0].fail();
		await expect(upload).rejects.toBeInstanceOf(APIError);
	});

	it.each([
		['empty', ''],
		['non-JSON', '<html>bad gateway</html>']
	])('rejects an APIError for an %s successful response', async (_name, responseText) => {
		const upload = uploadDocument(new File(['x'], 'p.pdf', { type: 'application/pdf' }));
		MockXMLHttpRequest.instances[0].respondRaw(200, responseText);

		await expect(upload).rejects.toMatchObject({ status: 200 });
	});

	it('rejects when the upload is aborted', async () => {
		const upload = uploadDocument(new File(['x'], 'p.pdf', { type: 'application/pdf' }));
		MockXMLHttpRequest.instances[0].abort();

		await expect(upload).rejects.toMatchObject({ status: 0, message: 'request aborted' });
	});

	it('rejects when the upload times out', async () => {
		const upload = uploadDocument(new File(['x'], 'p.pdf', { type: 'application/pdf' }));
		MockXMLHttpRequest.instances[0].timeout();

		await expect(upload).rejects.toMatchObject({ status: 0, message: 'request timed out' });
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

	it('treats a 404 on delete as already-deleted', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(404, { error: 'document not found' })));

		await expect(deleteDocument(1)).resolves.toBeUndefined();
	});

	it('still rejects on a non-404 delete failure', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(500, { error: 'boom' })));

		await expect(deleteDocument(1)).rejects.toBeInstanceOf(APIError);
	});

	it('loads the effective upload capabilities from the authenticated shared endpoint', async () => {
		const capabilities = {
			max_bytes: 20 * 1024 * 1024,
			rich_extraction: true,
			accept: 'application/pdf,.pdf,image/png,.png'
		};
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: capabilities }));
		vi.stubGlobal('fetch', fetchMock);

		await expect(getDocumentUploadCapabilities()).resolves.toEqual(capabilities);
		expect(fetchMock.mock.calls[0][0]).toBe('/api/documents/capabilities');
	});

	it('loads grouped visible document options for chat references', async () => {
		const options = {
			own: [sampleDoc],
			public: [{ ...sampleDoc, id: 2, filename: 'guide.pdf', scope: 'public' }]
		};
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: options }));
		vi.stubGlobal('fetch', fetchMock);

		await expect(listDocumentReferences()).resolves.toEqual(options);
		expect(fetchMock.mock.calls[0][0]).toBe('/api/documents/references');
	});
});
