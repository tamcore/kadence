import { describe, it, expect, vi, beforeEach } from 'vitest';
import { getOverview, searchTerm } from './context';
import { setCsrfToken, APIError } from './client';

function jsonResponse(status: number, body: unknown): Response {
	return new Response(status === 204 ? null : JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});
}

const sampleOverview = {
	documentCount: 2,
	documentChunkCount: 10,
	conversationChunkCount: 5,
	documents: [{ id: 1, filename: 'a.pdf', scope: 'private', createdAt: '2026-07-01T00:00:00Z' }],
	topTerms: [{ term: 'training', weight: 0.9, count: 4 }],
	reindex: { stale: 0, total: 10 }
};

const sampleSearch = {
	term: 'training',
	snippets: [{ content: 'some snippet', sourceKind: 'document', documentId: 1 }]
};

describe('context api', () => {
	beforeEach(() => {
		setCsrfToken('tok');
		vi.restoreAllMocks();
	});

	it('GETs /api/context/overview and returns the overview', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: sampleOverview }));
		vi.stubGlobal('fetch', fetchMock);

		const overview = await getOverview();

		expect(overview).toEqual(sampleOverview);
		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toBe('/api/context/overview');
		expect(init.method).toBe('GET');
		expect(init.credentials).toBe('include');
	});

	it('GETs /api/context/search with the term query-encoded', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: sampleSearch }));
		vi.stubGlobal('fetch', fetchMock);

		const result = await searchTerm('foo bar/baz');

		expect(result).toEqual(sampleSearch);
		const [url] = fetchMock.mock.calls[0];
		expect(url).toBe(`/api/context/search?term=${encodeURIComponent('foo bar/baz')}`);
	});

	it('throws APIError when the overview request fails', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(500, { error: 'boom' })));
		await expect(getOverview()).rejects.toMatchObject({ status: 500, message: 'boom' });
		expect(APIError).toBeDefined();
	});
});
