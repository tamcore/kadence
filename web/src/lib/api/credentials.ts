import { api } from '$lib/api/client';

// submitCredentials POSTs the entered credential values for a pending
// credentials_request. Values are sent only in the request body — callers
// must not persist them anywhere else (store, localStorage, URL). Routed
// through the shared api.post helper so this call gets the same
// reachability pre-check, CSRF handling, and probe-on-failure behaviour as
// every other API call.
export async function submitCredentials(
	requestId: string,
	values: Record<string, string>
): Promise<void> {
	await api.post<void>(`/credentials/${encodeURIComponent(requestId)}`, { values });
}
