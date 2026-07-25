import { api } from '$lib/api/client';

export type McpAuditSource = 'chat' | 'scheduled';
export type McpAuditStatus = 'running' | 'succeeded' | 'failed';

export interface McpAuditSummary {
	id: number;
	actorUserId: number;
	actorUsername: string;
	conversationId: string;
	source: McpAuditSource;
	scheduledTaskId?: string;
	scheduledRunId?: number;
	model: string;
	toolCallId: string;
	toolName: string;
	status: McpAuditStatus;
	startedAt: string;
	finishedAt?: string;
}

export interface McpAuditDetail extends McpAuditSummary {
	arguments: string;
	result: string;
	error: string;
}

export interface McpAuditPage {
	items: McpAuditSummary[];
	nextCursor?: string;
}

export interface McpAuditFilters {
	limit?: number;
	userId?: number;
	conversationId?: string;
	source?: McpAuditSource;
	status?: McpAuditStatus;
	model?: string;
	tool?: string;
	from?: string;
	to?: string;
	cursor?: string;
}

export function listMcpAuditCalls(filters: McpAuditFilters = {}): Promise<McpAuditPage> {
	const query = new URLSearchParams();
	for (const [key, value] of Object.entries(filters)) {
		if (value !== undefined && value !== '') query.set(key, String(value));
	}
	const suffix = query.size > 0 ? `?${query.toString()}` : '';
	return api.get<McpAuditPage>(`/admin/mcp-audit${suffix}`);
}

export function getMcpAuditCall(id: number): Promise<McpAuditDetail> {
	return api.get<McpAuditDetail>(`/admin/mcp-audit/${id}`);
}
