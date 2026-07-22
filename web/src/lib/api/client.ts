import { goto } from '$app/navigation';
import { clearAuth } from '$lib/stores/auth';
import type { User } from '$lib/types';

const API_BASE = '/api';
const LOGIN_PATH = '/login';

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
const REQUEST_TIMEOUT_MS = 15000;

// handleUnauthorized centralizes the reaction to a 401: drop local auth state and
// send the user back to /login with a returnTo, unless the failing request already
// originated from the login page (avoids a redirect loop on a failed login attempt).
export function handleUnauthorized(): void {
	if (typeof window === 'undefined') return;
	clearAuth();
	if (window.location.pathname === LOGIN_PATH) return;
	const returnTo = window.location.pathname + window.location.search;
	void goto(`${LOGIN_PATH}?returnTo=${encodeURIComponent(returnTo)}`);
}

async function request<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
	const method = (options.method ?? 'GET').toUpperCase();
	const headers: Record<string, string> = {
		'Content-Type': 'application/json',
		...(options.headers as Record<string, string> | undefined)
	};
	if (csrfToken && UNSAFE.has(method)) {
		headers['X-CSRF-Token'] = csrfToken;
	}

	const controller = new AbortController();
	const timer = setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);
	try {
		const resp = await fetch(`${API_BASE}${endpoint}`, {
			...options,
			method,
			credentials: 'include',
			headers,
			signal: controller.signal
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
			if (resp.status === 401) handleUnauthorized();
			throw new APIError(resp.status, envelope?.error ?? `request failed (${resp.status})`);
		}
		return (envelope?.data as T) ?? (undefined as T);
	} finally {
		clearTimeout(timer);
	}
}

export const api = {
	login: (username: string, password: string, remember: boolean) =>
		request<User>('/session', { method: 'POST', body: JSON.stringify({ username, password, remember }) }),
	logout: () => request<{ ok: boolean }>('/session', { method: 'DELETE' }),
	getCurrentUser: () => request<User>('/session'),
	listUsers: () => request<User[]>('/users'),
	createUser: (u: { username: string; email: string; password: string; role: string }) =>
		request<User>('/users', { method: 'POST', body: JSON.stringify(u) }),
	updateUser: (id: number, u: { username: string; email: string; role: string; password?: string }) =>
		request<User>(`/users/${id}`, { method: 'PATCH', body: JSON.stringify(u) }),
	deleteUser: (id: number) => request<{ ok: boolean }>(`/users/${id}`, { method: 'DELETE' }),
	get: <T,>(path: string) => request<T>(path),
	post: <T,>(path: string, body: unknown) => request<T>(path, { method: 'POST', body: JSON.stringify(body) }),
	put: <T,>(path: string, body: unknown) => request<T>(path, { method: 'PUT', body: JSON.stringify(body) }),
	patch: <T,>(path: string, body: unknown) => request<T>(path, { method: 'PATCH', body: JSON.stringify(body) }),
	del: <T,>(path: string) => request<T>(path, { method: 'DELETE' })
};
