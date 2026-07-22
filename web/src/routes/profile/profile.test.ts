import { render, screen, fireEvent, waitFor, within } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('$lib/api/profile', () => ({
	updateProfile: vi.fn(),
	changePassword: vi.fn()
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
});
