import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { readable } from 'svelte/store';
import { afterEach, describe, expect, it, vi } from 'vitest';

const listMock = vi.fn();
const createMock = vi.fn();
const updateMock = vi.fn();
const deleteMock = vi.fn();
vi.mock('$lib/api/client', () => ({
	api: {
		listUsers: () => listMock(),
		createUser: (u: unknown) => createMock(u),
		updateUser: (id: number, u: unknown) => updateMock(id, u),
		deleteUser: (id: number) => deleteMock(id)
	}
}));
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
// Force admin so the guard doesn't redirect.
vi.mock('$lib/stores/auth', () => ({ isAdmin: readable(true) }));

import Users from './+page.svelte';

afterEach(() => vi.clearAllMocks());

describe('admin users page', () => {
	it('lists users returned by the API', async () => {
		listMock.mockResolvedValueOnce([
			{ id: 1, username: 'alice', email: 'a@x.io', role: 'admin' },
			{ id: 2, username: 'bob', email: 'b@x.io', role: 'user' }
		]);
		render(Users);
		await waitFor(() => expect(screen.getByText('alice')).toBeInTheDocument());
		expect(screen.getByText('bob')).toBeInTheDocument();
	});

	it('does not show a create form until "New user" is clicked', async () => {
		listMock.mockResolvedValueOnce([]);
		render(Users);
		await waitFor(() => expect(screen.queryByText('Loading…')).not.toBeInTheDocument());
		expect(screen.queryByText('Create user')).not.toBeInTheDocument();

		await fireEvent.click(screen.getByText('New user'));
		expect(await screen.findByText('Create user')).toBeInTheDocument();
	});

	it('opens a prefilled edit modal and saves via updateUser', async () => {
		listMock.mockResolvedValue([{ id: 2, username: 'bob', email: 'b@x.io', role: 'user' }]);
		updateMock.mockResolvedValue({});
		render(Users);
		await waitFor(() => expect(screen.getByText('bob')).toBeInTheDocument());

		await fireEvent.click(screen.getByText('Edit'));
		expect(await screen.findByDisplayValue('bob')).toBeInTheDocument();
		expect(screen.getByDisplayValue('b@x.io')).toBeInTheDocument();

		await fireEvent.click(screen.getByText('Save changes'));
		await waitFor(() =>
			expect(updateMock).toHaveBeenCalledWith(
				2,
				expect.objectContaining({ username: 'bob', email: 'b@x.io', role: 'user' })
			)
		);
	});
});
