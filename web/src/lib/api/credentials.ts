import { getCsrfToken, setCsrfToken, APIError } from '$lib/api/client';

interface Envelope<T> {
	data: T;
	error?: string;
}

// submitCredentials POSTs the entered credential values for a pending
// credentials_request. Values are sent only in the request body — callers
// must not persist them anywhere else (store, localStorage, URL).
export async function submitCredentials(
	requestId: string,
	values: Record<string, string>
): Promise<void> {
	const headers: Record<string, string> = { 'Content-Type': 'application/json' };
	const token = getCsrfToken();
	if (token) headers['X-CSRF-Token'] = token;

	const resp = await fetch(`/api/credentials/${requestId}`, {
		method: 'POST',
		credentials: 'include',
		headers,
		body: JSON.stringify({ values })
	});

	const rotated = resp.headers.get('X-CSRF-Token');
	if (rotated) setCsrfToken(rotated);

	if (!resp.ok) {
		let envelope: Envelope<unknown> | null = null;
		try {
			envelope = (await resp.json()) as Envelope<unknown>;
		} catch {
			envelope = null;
		}
		throw new APIError(resp.status, envelope?.error ?? `credential submission failed (${resp.status})`);
	}
}
