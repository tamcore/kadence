import { describe, it, expect, vi, beforeEach } from 'vitest';
import { updateProfile, changePassword } from './profile';
import { setCsrfToken, APIError } from './client';

function jsonResponse(status: number, body: unknown): Response {
	return new Response(status === 204 ? null : JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});
}

const sampleUser = {
	id: 1,
	username: 'alice',
	email: 'alice@example.com',
	role: 'user',
	displayName: 'Alice',
	unitSystem: 'metric',
	location: '',
	aboutMe: ''
};

describe('profile api', () => {
	beforeEach(() => {
		setCsrfToken('tok');
		vi.restoreAllMocks();
	});

	it('PATCHes /api/profile with all three fields and returns the updated user', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: sampleUser }));
		vi.stubGlobal('fetch', fetchMock);

		const user = await updateProfile({
			displayName: 'Alice',
			email: 'alice@example.com',
			unitSystem: 'metric'
		});

		expect(user).toEqual(sampleUser);
		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toBe('/api/profile');
		expect(init.method).toBe('PATCH');
		expect(init.credentials).toBe('include');
		expect(init.headers['X-CSRF-Token']).toBe('tok');
		expect(JSON.parse(init.body)).toEqual({
			displayName: 'Alice',
			email: 'alice@example.com',
			unitSystem: 'metric'
		});
	});

	it('PATCHes /api/profile with location and aboutMe when provided', async () => {
		const fetchMock = vi
			.fn()
			.mockResolvedValue(jsonResponse(200, { data: { ...sampleUser, location: 'Berlin', aboutMe: 'runs marathons' } }));
		vi.stubGlobal('fetch', fetchMock);

		await updateProfile({
			displayName: 'Alice',
			email: 'alice@example.com',
			unitSystem: 'metric',
			location: 'Berlin',
			aboutMe: 'runs marathons'
		});

		const [, init] = fetchMock.mock.calls[0];
		expect(JSON.parse(init.body)).toEqual({
			displayName: 'Alice',
			email: 'alice@example.com',
			unitSystem: 'metric',
			location: 'Berlin',
			aboutMe: 'runs marathons'
		});
	});

	it('throws APIError with the message on email conflict (409)', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(409, { error: 'email already in use' })));
		await expect(
			updateProfile({ displayName: 'Alice', email: 'taken@example.com', unitSystem: 'metric' })
		).rejects.toMatchObject({ status: 409, message: 'email already in use' });
	});

	it('POSTs /api/profile/password with currentPassword, newPassword, logoutOthers', async () => {
		const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: null }));
		vi.stubGlobal('fetch', fetchMock);

		await changePassword({ currentPassword: 'old', newPassword: 'newpass123', logoutOthers: true });

		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toBe('/api/profile/password');
		expect(init.method).toBe('POST');
		expect(JSON.parse(init.body)).toEqual({
			currentPassword: 'old',
			newPassword: 'newpass123',
			logoutOthers: true
		});
	});

	it('throws APIError with the message on wrong current password (403)', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(403, { error: 'incorrect current password' })));
		await expect(
			changePassword({ currentPassword: 'wrong', newPassword: 'newpass123', logoutOthers: false })
		).rejects.toMatchObject({ status: 403, message: 'incorrect current password' });
		expect(APIError).toBeDefined();
	});
});
