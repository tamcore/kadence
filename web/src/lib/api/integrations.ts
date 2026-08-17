import { api } from '$lib/api/client';

/** LinkStatus mirrors the server's stored link states. */
export type LinkStatus = 'linked' | 'reauth_required' | 'disconnect_pending';

export interface Integration {
	server: string;
	linked: boolean;
	status?: LinkStatus;
	scope?: string;
	access_expires_at?: string;
}

export interface StartLinkResponse {
	authorize_url: string;
}

export function listIntegrations(): Promise<Integration[]> {
	return api.get<Integration[]>('/mcp/integrations');
}

export function startLink(server: string): Promise<StartLinkResponse> {
	return api.post<StartLinkResponse>(`/mcp/oauth/${encodeURIComponent(server)}/start`, {});
}

export function unlinkIntegration(server: string): Promise<void> {
	return api.del(`/mcp/oauth/${encodeURIComponent(server)}`);
}

/** A human label for an integration id. An unknown id is shown capitalized. */
export function integrationLabel(server: string): string {
	const known: Record<string, string> = { garmin: 'Garmin Connect' };
	return known[server] ?? server.charAt(0).toUpperCase() + server.slice(1);
}
