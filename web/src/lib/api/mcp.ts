import { api } from '$lib/api/client';

export interface McpServer {
	name: string;
	transport: string;
	scope: 'global' | 'user';
	state: 'healthy' | 'unhealthy' | 'checking';
	toolCount: number;
	checkedAt?: string;
	error?: string;
	url?: string;
	id?: number;
	editable: boolean;
}

export interface McpList {
	servers: McpServer[];
	canAdd: boolean;
}

export interface McpInput {
	name: string;
	url: string;
	transport: string;
	authUser: string;
	authPass: string;
}

export interface McpTool {
	name: string;
	description: string;
	inputSchema?: unknown;
}

export interface McpToolList {
	name: string;
	tools: McpTool[];
}

export function listMcp(): Promise<McpList> {
	return api.get<McpList>('/mcp');
}

export function createMcp(input: McpInput): Promise<unknown> {
	return api.post('/mcp', input);
}

export function updateMcp(id: number, input: McpInput): Promise<unknown> {
	return api.put(`/mcp/${id}`, input);
}

export function deleteMcp(id: number): Promise<unknown> {
	return api.del(`/mcp/${id}`);
}

export function getMcpTools(name: string): Promise<McpToolList> {
	return api.get<McpToolList>(`/mcp/${encodeURIComponent(name)}/tools`);
}
