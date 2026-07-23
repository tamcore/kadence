import { api } from '$lib/api/client';
import type { User } from '$lib/types';

export interface ProfileInput {
	displayName: string;
	email: string;
	unitSystem: 'metric' | 'imperial';
	location?: string;
	aboutMe?: string;
}

export interface PasswordInput {
	currentPassword: string;
	newPassword: string;
	logoutOthers: boolean;
}

export function updateProfile(input: ProfileInput): Promise<User> {
	return api.patch<User>('/profile', input);
}

export function changePassword(input: PasswordInput): Promise<unknown> {
	return api.post('/profile/password', input);
}
