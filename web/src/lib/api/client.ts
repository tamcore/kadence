import type { User } from '$lib/types';

const API_BASE = '/api';

export class APIError extends Error {
	status: number;
	constructor(status: number, message: string) {
		super(message);
		this.name = 'APIError';
		this.status = status;
	}
}

interface Envelope<T> {
	data: T;
	error?: string;
}

let csrfToken: string | null = null;
export function getCsrfToken(): string | null {
	return csrfToken;
}
export function setCsrfToken(v: string | null): void {
	csrfToken = v;
}

const UNSAFE = new Set(['POST', 'PUT', 'PATCH', 'DELETE']);

async function request<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
	const method = (options.method ?? 'GET').toUpperCase();
	const headers: Record<string, string> = {
		'Content-Type': 'application/json',
		...(options.headers as Record<string, string> | undefined)
	};
	if (csrfToken && UNSAFE.has(method)) {
		headers['X-CSRF-Token'] = csrfToken;
	}

	const resp = await fetch(`${API_BASE}${endpoint}`, {
		...options,
		method,
		credentials: 'include',
		headers
	});

	const tok = resp.headers.get('X-CSRF-Token');
	if (tok) setCsrfToken(tok);

	let envelope: Envelope<T> | null = null;
	if (resp.headers.get('content-length') !== '0' && resp.status !== 204) {
		try {
			envelope = (await resp.json()) as Envelope<T>;
		} catch {
			envelope = null;
		}
	}

	if (!resp.ok) {
		throw new APIError(resp.status, envelope?.error ?? `request failed (${resp.status})`);
	}
	return (envelope?.data as T) ?? (undefined as T);
}

export const api = {
	login: (username: string, password: string, remember: boolean) =>
		request<User>('/session', { method: 'POST', body: JSON.stringify({ username, password, remember }) }),
	logout: () => request<{ ok: boolean }>('/session', { method: 'DELETE' }),
	getCurrentUser: () => request<User>('/session'),
	listUsers: () => request<User[]>('/users'),
	createUser: (u: { username: string; email: string; password: string; role: string }) =>
		request<User>('/users', { method: 'POST', body: JSON.stringify(u) }),
	deleteUser: (id: number) => request<{ ok: boolean }>(`/users/${id}`, { method: 'DELETE' })
};
