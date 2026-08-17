import { api } from '$lib/api/client';

// submitConfirmation answers a pending confirm_request. The tool call that
// raised it is blocked on this answer, and the server stops waiting after a
// short window — so a decline must be sent explicitly rather than by silence.
//
// Routed through the shared api.post helper for the same reachability
// pre-check, CSRF handling, and probe-on-failure behaviour as every other call.
export async function submitConfirmation(requestId: string, confirm: boolean): Promise<void> {
	await api.post<void>(`/confirmations/${encodeURIComponent(requestId)}`, { confirm });
}
