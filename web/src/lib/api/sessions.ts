import { api } from '$lib/api/client';

export interface Session {
	publicId: string;
	device: string;
	ip: string;
	createdAt: string;
	lastSeenAt: string;
	current: boolean;
}

export function listSessions(): Promise<Session[]> {
	return api.get<Session[]>('/sessions');
}

export function revokeSession(publicId: string): Promise<unknown> {
	return api.del(`/sessions/${encodeURIComponent(publicId)}`);
}

export function revokeOtherSessions(): Promise<unknown> {
	return api.post('/sessions/revoke-others', {});
}
