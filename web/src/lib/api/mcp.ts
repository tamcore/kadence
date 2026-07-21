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

export function listMcp(): Promise<McpServer[]> {
	return api.get<McpServer[]>('/mcp');
}

export function getMcpTools(name: string): Promise<McpToolList> {
	return api.get<McpToolList>(`/mcp/${encodeURIComponent(name)}/tools`);
}
