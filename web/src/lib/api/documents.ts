import { api, getCsrfToken, setCsrfToken, APIError } from '$lib/api/client';
import type { Document } from '$lib/types';

interface Envelope<T> {
	data: T;
	error?: string;
}

function documentsPath(admin: boolean | undefined): string {
	return admin ? '/admin/documents' : '/documents';
}

// uploadDocument POSTs a file as multipart/form-data. It cannot use the shared
// JSON `request` helper (which forces a JSON content-type), so it replicates the
// CSRF + credentials handling directly (see streamChat for the same pattern).
export async function uploadDocument(file: File, opts: { admin?: boolean } = {}): Promise<Document> {
	const form = new FormData();
	form.append('file', file);

	const headers: Record<string, string> = {};
	const token = getCsrfToken();
	if (token) headers['X-CSRF-Token'] = token;

	const resp = await fetch(`/api${documentsPath(opts.admin)}`, {
		method: 'POST',
		credentials: 'include',
		headers, // NOTE: no Content-Type — the browser sets the multipart boundary
		body: form
	});

	const rotated = resp.headers.get('X-CSRF-Token');
	if (rotated) setCsrfToken(rotated);

	let envelope: Envelope<Document> | null = null;
	if (resp.status !== 204) {
		try {
			envelope = (await resp.json()) as Envelope<Document>;
		} catch {
			envelope = null;
		}
	}
	if (!resp.ok) {
		throw new APIError(resp.status, envelope?.error ?? `upload failed (${resp.status})`);
	}
	return envelope!.data;
}

export function listDocuments(opts: { admin?: boolean } = {}): Promise<Document[]> {
	return api.get<Document[]>(documentsPath(opts.admin));
}

export function deleteDocument(id: number, opts: { admin?: boolean } = {}): Promise<void> {
	return api.del<void>(`${documentsPath(opts.admin)}/${id}`);
}
