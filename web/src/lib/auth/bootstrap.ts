import { APIError } from '$lib/api/client';
import type { User } from '$lib/types';

export type SessionBootstrapResult = 'authenticated' | 'unauthorized' | 'unavailable';

interface AuthState {
	setAuth(user: User): void;
	clearAuth(): void;
}

export async function bootstrapSession(
	getCurrentUser: () => Promise<User>,
	auth: AuthState
): Promise<SessionBootstrapResult> {
	try {
		auth.setAuth(await getCurrentUser());
		return 'authenticated';
	} catch (error) {
		if (error instanceof APIError && error.status === 401) {
			auth.clearAuth();
			return 'unauthorized';
		}
		return 'unavailable';
	}
}
