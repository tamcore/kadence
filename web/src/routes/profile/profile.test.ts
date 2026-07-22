import { render, screen, fireEvent, waitFor, within } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

const updateProfileMock = vi.fn();
const changePasswordMock = vi.fn();
vi.mock('$lib/api/profile', () => ({
	updateProfile: (...a: unknown[]) => updateProfileMock(...a),
	changePassword: (...a: unknown[]) => changePasswordMock(...a)
}));

const listSessionsMock = vi.fn().mockResolvedValue([]);
const revokeSessionMock = vi.fn();
const revokeOtherSessionsMock = vi.fn();
vi.mock('$lib/api/sessions', () => ({
	listSessions: (...a: unknown[]) => listSessionsMock(...a),
	revokeSession: (...a: unknown[]) => revokeSessionMock(...a),
	revokeOtherSessions: (...a: unknown[]) => revokeOtherSessionsMock(...a)
}));

const isWebAuthnEnabledMock = vi.fn().mockResolvedValue(true);
const listPasskeysMock = vi.fn().mockResolvedValue([]);
const registerPasskeyMock = vi.fn();
const renamePasskeyMock = vi.fn();
const deletePasskeyMock = vi.fn();
vi.mock('$lib/api/webauthn', () => ({
	isWebAuthnEnabled: (...a: unknown[]) => isWebAuthnEnabledMock(...a),
	listPasskeys: (...a: unknown[]) => listPasskeysMock(...a),
	registerPasskey: (...a: unknown[]) => registerPasskeyMock(...a),
	renamePasskey: (...a: unknown[]) => renamePasskeyMock(...a),
	deletePasskey: (...a: unknown[]) => deletePasskeyMock(...a)
}));

import Page from './+page.svelte';

afterEach(() => vi.clearAllMocks());

describe('/profile', () => {
	it('opens a name modal (not window.prompt) to add a passkey, and submits the entered name', async () => {
		listPasskeysMock.mockResolvedValue([]);
		registerPasskeyMock.mockResolvedValue(undefined);
		render(Page);

		await waitFor(() => expect(screen.getByText('Add a passkey')).toBeInTheDocument());
		await fireEvent.click(screen.getByText('Add a passkey'));

		const dialog = await screen.findByRole('dialog', { name: 'Name this passkey' });
		const input = within(dialog).getByLabelText('Name');
		await fireEvent.input(input, { target: { value: 'My laptop' } });
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

		await waitFor(() => expect(registerPasskeyMock).toHaveBeenCalledWith('My laptop'));
	});

	it('opens a rename modal prefilled with the current name', async () => {
		listPasskeysMock.mockResolvedValue([
			{ publicId: 'pk-1', name: 'Old name', createdAt: '2026-01-01T00:00:00Z', lastUsedAt: null }
		]);
		renamePasskeyMock.mockResolvedValue(undefined);
		render(Page);

		await waitFor(() => expect(screen.getByText('Old name')).toBeInTheDocument());
		await fireEvent.click(screen.getByRole('button', { name: 'Rename' }));

		const dialog = await screen.findByRole('dialog', { name: 'Rename passkey' });
		const input = within(dialog).getByLabelText('Name') as HTMLInputElement;
		expect(input.value).toBe('Old name');

		await fireEvent.input(input, { target: { value: 'New name' } });
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

		await waitFor(() => expect(renamePasskeyMock).toHaveBeenCalledWith('pk-1', 'New name'));
	});

	it('asks for confirmation before deleting a passkey', async () => {
		listPasskeysMock.mockResolvedValue([
			{ publicId: 'pk-1', name: 'Old name', createdAt: '2026-01-01T00:00:00Z', lastUsedAt: null }
		]);
		render(Page);

		await waitFor(() => expect(screen.getByText('Old name')).toBeInTheDocument());
		await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));

		const dialog = await screen.findByRole('dialog', { name: 'Delete passkey' });
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
		expect(deletePasskeyMock).not.toHaveBeenCalled();

		await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
		const dialog2 = await screen.findByRole('dialog', { name: 'Delete passkey' });
		await fireEvent.click(within(dialog2).getByRole('button', { name: 'Delete' }));
		await waitFor(() => expect(deletePasskeyMock).toHaveBeenCalledWith('pk-1'));
	});

	it('asks for confirmation before signing out other devices', async () => {
		revokeOtherSessionsMock.mockResolvedValue(undefined);
		render(Page);

		await waitFor(() => expect(screen.getByText('Sign out other devices')).toBeInTheDocument());
		await fireEvent.click(screen.getByText('Sign out other devices'));

		const dialog = await screen.findByRole('dialog', { name: 'Sign out other devices' });
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
		expect(revokeOtherSessionsMock).not.toHaveBeenCalled();

		await fireEvent.click(screen.getByText('Sign out other devices'));
		const dialog2 = await screen.findByRole('dialog', { name: 'Sign out other devices' });
		await fireEvent.click(within(dialog2).getByRole('button', { name: 'Sign out others' }));
		await waitFor(() => expect(revokeOtherSessionsMock).toHaveBeenCalled());
	});

	it('lists sessions and revokes a single non-current session', async () => {
		listSessionsMock
			.mockResolvedValueOnce([
				{
					publicId: 'pub-1',
					device: 'Chrome on macOS',
					ip: '203.0.113.10',
					createdAt: '2026-07-01T12:00:00Z',
					lastSeenAt: '2026-07-21T09:00:00Z',
					current: true
				},
				{
					publicId: 'pub-2',
					device: 'Safari on iOS',
					ip: '203.0.113.20',
					createdAt: '2026-06-15T08:00:00Z',
					lastSeenAt: '2026-07-20T18:30:00Z',
					current: false
				}
			])
			.mockResolvedValueOnce([
				{
					publicId: 'pub-1',
					device: 'Chrome on macOS',
					ip: '203.0.113.10',
					createdAt: '2026-07-01T12:00:00Z',
					lastSeenAt: '2026-07-21T09:00:00Z',
					current: true
				}
			]);
		revokeSessionMock.mockResolvedValue(undefined);
		render(Page);

		await waitFor(() => expect(screen.getByText('Safari on iOS')).toBeInTheDocument());
		expect(screen.getByText('This device')).toBeInTheDocument();

		await fireEvent.click(screen.getByRole('button', { name: 'Revoke' }));

		await waitFor(() => expect(revokeSessionMock).toHaveBeenCalledWith('pub-2'));
		await waitFor(() => expect(screen.queryByText('Safari on iOS')).not.toBeInTheDocument());
	});

	it('surfaces an error message when revoking a session fails', async () => {
		listSessionsMock.mockResolvedValue([
			{
				publicId: 'pub-2',
				device: 'Safari on iOS',
				ip: '203.0.113.20',
				createdAt: '2026-06-15T08:00:00Z',
				lastSeenAt: '2026-07-20T18:30:00Z',
				current: false
			}
		]);
		revokeSessionMock.mockRejectedValueOnce(new Error('revoke failed on server'));
		render(Page);

		await waitFor(() => expect(screen.getByText('Safari on iOS')).toBeInTheDocument());
		await fireEvent.click(screen.getByRole('button', { name: 'Revoke' }));

		expect(await screen.findByRole('alert')).toHaveTextContent('revoke failed on server');
	});

	it('changes the password, clears the form, and shows a success message', async () => {
		listSessionsMock.mockResolvedValue([]);
		listPasskeysMock.mockResolvedValue([]);
		changePasswordMock.mockResolvedValueOnce(undefined);
		render(Page);

		const currentPw = screen.getByLabelText('Current password') as HTMLInputElement;
		const newPw = screen.getByLabelText('New password') as HTMLInputElement;
		await fireEvent.input(currentPw, { target: { value: 'oldpass1' } });
		await fireEvent.input(newPw, { target: { value: 'newpass1' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Change password' }));

		await waitFor(() =>
			expect(changePasswordMock).toHaveBeenCalledWith({
				currentPassword: 'oldpass1',
				newPassword: 'newpass1',
				logoutOthers: true
			})
		);
		await waitFor(() => expect(screen.getByText('Password changed')).toBeInTheDocument());
		expect(currentPw.value).toBe('');
		expect(newPw.value).toBe('');
	});

	it('surfaces an error message when the password change fails', async () => {
		listSessionsMock.mockResolvedValue([]);
		listPasskeysMock.mockResolvedValue([]);
		changePasswordMock.mockRejectedValueOnce(new Error('incorrect current password'));
		render(Page);

		await fireEvent.input(screen.getByLabelText('Current password'), { target: { value: 'wrong' } });
		await fireEvent.input(screen.getByLabelText('New password'), { target: { value: 'newpass1' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Change password' }));

		expect(await screen.findByRole('alert')).toHaveTextContent('incorrect current password');
	});

	it('saves preferences (unit system) via the shared profile update path', async () => {
		listSessionsMock.mockResolvedValue([]);
		listPasskeysMock.mockResolvedValue([]);
		updateProfileMock.mockResolvedValueOnce({
			id: 1,
			username: 'u',
			displayName: 'u',
			email: 'u@example.com',
			role: 'user',
			unitSystem: 'imperial'
		});
		render(Page);

		await fireEvent.click(screen.getByRole('radio', { name: 'Imperial' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Save preferences' }));

		await waitFor(() =>
			expect(updateProfileMock).toHaveBeenCalledWith(
				expect.objectContaining({ unitSystem: 'imperial' })
			)
		);
		await waitFor(() => expect(screen.getByText('Saved')).toBeInTheDocument());
	});

	it('surfaces an error message when saving preferences fails', async () => {
		listSessionsMock.mockResolvedValue([]);
		listPasskeysMock.mockResolvedValue([]);
		updateProfileMock.mockRejectedValueOnce(new Error('email already in use'));
		render(Page);

		await fireEvent.click(screen.getByRole('button', { name: 'Save preferences' }));

		expect(await screen.findByRole('alert')).toHaveTextContent('email already in use');
	});
});
