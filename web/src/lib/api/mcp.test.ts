import { describe, it, expect, vi, beforeEach } from 'vitest';
import { listMcp, createMcp, updateMcp, deleteMcp, getMcpTools } from './mcp';
import { setCsrfToken, APIError } from './client';

function jsonResponse(status: number, body: unknown): Response {
	return new Response(status === 204 ? null : JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});
}

const sampleServer = {
	name: 'search',
	transport: 'streamable-http',
	scope: 'global' as const,
	state: 'healthy' as const,
	toolCount: 3,
	editable: true
};

const sampleInput = {
	name: 'search',
	url: 'https://mcp.example.com',
	transport: 'streamable-http',
	authUser: 'u',
	authPass: 'p'
};

describe('mcp api', () => {
	beforeEach(() => {
		setCsrfToken('tok');
		vi.restoreAllMocks();
	});

	it('GETs /api/mcp and returns the server list', async () => {
		const fetchMock = vi
			.fn()
			.mockResolvedValue(jsonResponse(200, { data: { servers: [sampleServer], canAdd: true } }));
		vi.stubGlobal('fetch', fetchMock);

		const list = await listMcp();

		expect(list.servers).toEqual([sampleServer]);
		expect(list.canAdd).toBe(true);
		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toBe('/api/mcp');
		expect(init.method).toBe('GET');
	});

	it('POSTs /api/mcp with the CSRF header to create a server', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: sampleServer }));
		vi.stubGlobal('fetch', fetchMock);

		await createMcp(sampleInput);

		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toBe('/api/mcp');
		expect(init.method).toBe('POST');
		expect(JSON.parse(init.body)).toEqual(sampleInput);
		expect(init.headers['X-CSRF-Token']).toBe('tok');
	});

	it('PUTs /api/mcp/{id} to update a server', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: sampleServer }));
		vi.stubGlobal('fetch', fetchMock);

		await updateMcp(7, sampleInput);

		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toBe('/api/mcp/7');
		expect(init.method).toBe('PUT');
		expect(JSON.parse(init.body)).toEqual(sampleInput);
	});

	it('DELETEs /api/mcp/{id}', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: null }));
		vi.stubGlobal('fetch', fetchMock);

		await deleteMcp(7);

		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toBe('/api/mcp/7');
		expect(init.method).toBe('DELETE');
	});

	it('GETs /api/mcp/{name}/tools with the name path-encoded', async () => {
		const fetchMock = vi
			.fn()
			.mockResolvedValue(jsonResponse(200, { data: { name: 'a/b', tools: [] } }));
		vi.stubGlobal('fetch', fetchMock);

		const result = await getMcpTools('a/b');

		expect(result.name).toBe('a/b');
		const [url] = fetchMock.mock.calls[0];
		expect(url).toBe(`/api/mcp/${encodeURIComponent('a/b')}/tools`);
	});

	it('throws APIError when creating a server fails validation', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(400, { error: 'invalid url' })));
		await expect(createMcp(sampleInput)).rejects.toMatchObject({ status: 400, message: 'invalid url' });
		expect(APIError).toBeDefined();
	});
});
