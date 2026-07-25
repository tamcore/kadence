import { beforeEach, describe, expect, it, vi } from 'vitest';
import { getMcpAuditCall, listMcpAuditCalls } from './mcpAudit';

function response(data: unknown): Response {
	return new Response(JSON.stringify({ data }), {
		status: 200,
		headers: { 'Content-Type': 'application/json' }
	});
}

describe('MCP audit API', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
	});

	it('serializes list filters and cursor', async () => {
		const fetchMock = vi.fn().mockResolvedValue(response({ items: [], nextCursor: 'next' }));
		vi.stubGlobal('fetch', fetchMock);

		await listMcpAuditCalls({
			limit: 25,
			userId: 7,
			conversationId: '11111111-1111-4111-8111-111111111111',
			source: 'chat',
			status: 'succeeded',
			model: 'coach',
			tool: 'garmin__activities',
			from: '2026-07-25T10:00:00.000Z',
			to: '2026-07-25T12:00:00.000Z',
			cursor: 'next page'
		});

		expect(fetchMock).toHaveBeenCalledOnce();
		const url = new URL(fetchMock.mock.calls[0][0], 'https://kadence.test');
		expect(url.pathname).toBe('/api/admin/mcp-audit');
		expect(Object.fromEntries(url.searchParams)).toEqual({
			limit: '25',
			userId: '7',
			conversationId: '11111111-1111-4111-8111-111111111111',
			source: 'chat',
			status: 'succeeded',
			model: 'coach',
			tool: 'garmin__activities',
			from: '2026-07-25T10:00:00.000Z',
			to: '2026-07-25T12:00:00.000Z',
			cursor: 'next page'
		});
	});

	it('loads full call detail', async () => {
		const fetchMock = vi.fn().mockResolvedValue(response({ id: 42 }));
		vi.stubGlobal('fetch', fetchMock);

		await expect(getMcpAuditCall(42)).resolves.toMatchObject({ id: 42 });
		expect(fetchMock.mock.calls[0][0]).toBe('/api/admin/mcp-audit/42');
	});
});
