import { api, getCsrfToken, handleUnauthorized, setCsrfToken, APIError } from '$lib/api/client';
import type { Document } from '$lib/types';

interface Envelope<T> {
	data: T;
	error?: string;
}

export interface DocumentUploadCapabilities {
	max_bytes: number;
	rich_extraction: boolean;
	accept: string;
}

export interface DocumentReferenceOptions {
	own: Document[];
	public: Document[];
}

export interface DocumentUploadOptions {
	admin?: boolean;
	onUploadComplete?: () => void;
}

function documentsPath(admin: boolean | undefined): string {
	return admin ? '/admin/documents' : '/documents';
}

// uploadDocument POSTs a file as multipart/form-data. It cannot use the shared
// JSON `request` helper (which forces a JSON content-type), so it replicates the
// CSRF + credentials handling directly (see streamChat for the same pattern).
export function uploadDocument(file: File, opts: DocumentUploadOptions = {}): Promise<Document> {
	const form = new FormData();
	form.append('file', file);
	return new Promise<Document>((resolve, reject) => {
		const xhr = new XMLHttpRequest();
		xhr.open('POST', `/api${documentsPath(opts.admin)}`);
		xhr.withCredentials = true;
		const token = getCsrfToken();
		if (token) xhr.setRequestHeader('X-CSRF-Token', token);
		xhr.upload.onload = () => opts.onUploadComplete?.();
		xhr.onerror = () => reject(new APIError(0, 'network request failed'));
		xhr.onabort = () => reject(new APIError(0, 'request aborted'));
		xhr.ontimeout = () => reject(new APIError(0, 'request timed out'));
		xhr.onload = () => {
			const rotated = xhr.getResponseHeader('X-CSRF-Token');
			if (rotated) setCsrfToken(rotated);
			let envelope: Envelope<Document> | null = null;
			if (xhr.status !== 204 && xhr.responseText) {
				try {
					envelope = JSON.parse(xhr.responseText) as Envelope<Document>;
				} catch {
					envelope = null;
				}
			}
			if (xhr.status < 200 || xhr.status >= 300) {
				if (xhr.status === 401) handleUnauthorized();
				reject(new APIError(xhr.status, envelope?.error ?? `upload failed (${xhr.status})`));
				return;
			}
			if (typeof envelope !== 'object' || envelope === null || !('data' in envelope)) {
				reject(new APIError(xhr.status, 'upload failed (invalid response)'));
				return;
			}
			resolve(envelope.data);
		};
		xhr.send(form);
	});
}

export function listDocuments(opts: { admin?: boolean } = {}): Promise<Document[]> {
	return api.get<Document[]>(documentsPath(opts.admin));
}

export function getDocumentUploadCapabilities(): Promise<DocumentUploadCapabilities> {
	return api.get<DocumentUploadCapabilities>('/documents/capabilities');
}

export function listDocumentReferences(): Promise<DocumentReferenceOptions> {
	return api.get<DocumentReferenceOptions>('/documents/references');
}

// deleteDocument treats a 404 as success: the server (correctly) reports one for
// a document that is already gone, and delete is idempotent from the UI's point
// of view — a double-click must not surface an error for work that is done.
export async function deleteDocument(id: number, opts: { admin?: boolean } = {}): Promise<void> {
	try {
		await api.del<void>(`${documentsPath(opts.admin)}/${id}`);
	} catch (error: unknown) {
		if (error instanceof APIError && error.status === 404) return;
		throw error;
	}
}
