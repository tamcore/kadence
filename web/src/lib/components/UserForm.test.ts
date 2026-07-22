import { render, screen, fireEvent } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

const createUserMock = vi.fn();
const updateUserMock = vi.fn();

vi.mock('$lib/api/client', () => ({
	api: {
		createUser: (...a: unknown[]) => createUserMock(...a),
		updateUser: (...a: unknown[]) => updateUserMock(...a)
	},
	APIError: class APIError extends Error {}
}));

import UserForm from './UserForm.svelte';

const existingUser = {
	id: 5,
	username: 'bob',
	email: 'bob@example.com',
	role: 'user' as const,
	displayName: 'Bob',
	unitSystem: 'metric' as const
};

afterEach(() => {
	vi.clearAllMocks();
});

describe('UserForm', () => {
	it('renders empty fields in create mode', () => {
		render(UserForm, { mode: 'create', onSuccess: vi.fn(), onCancel: vi.fn() });
		expect((screen.getByLabelText('Username') as HTMLInputElement).value).toBe('');
		expect(screen.getByRole('button', { name: 'Create user' })).toBeInTheDocument();
		expect(screen.getByLabelText('Password')).toBeRequired();
	});

	it('prefills fields in edit mode and makes password optional', () => {
		render(UserForm, { mode: 'edit', user: existingUser, onSuccess: vi.fn(), onCancel: vi.fn() });
		expect((screen.getByLabelText('Username') as HTMLInputElement).value).toBe('bob');
		expect((screen.getByLabelText('Email') as HTMLInputElement).value).toBe('bob@example.com');
		expect(screen.getByLabelText('New password (leave blank to keep)')).not.toBeRequired();
		expect(screen.getByRole('button', { name: 'Save changes' })).toBeInTheDocument();
	});

	it('creates a user with the entered fields and calls onSuccess', async () => {
		createUserMock.mockResolvedValueOnce(undefined);
		const onSuccess = vi.fn();
		render(UserForm, { mode: 'create', onSuccess, onCancel: vi.fn() });

		await fireEvent.input(screen.getByLabelText('Username'), { target: { value: 'newuser' } });
		await fireEvent.input(screen.getByLabelText('Email'), { target: { value: 'new@example.com' } });
		await fireEvent.input(screen.getByLabelText('Password'), { target: { value: 'hunter2' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Create user' }));

		expect(createUserMock).toHaveBeenCalledWith({
			username: 'newuser',
			email: 'new@example.com',
			password: 'hunter2',
			role: 'user'
		});
		expect(onSuccess).toHaveBeenCalled();
	});

	it('updates a user, omitting password when left blank', async () => {
		updateUserMock.mockResolvedValueOnce(undefined);
		const onSuccess = vi.fn();
		render(UserForm, { mode: 'edit', user: existingUser, onSuccess, onCancel: vi.fn() });

		await fireEvent.input(screen.getByLabelText('Email'), { target: { value: 'bob2@example.com' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));

		expect(updateUserMock).toHaveBeenCalledWith(5, {
			username: 'bob',
			email: 'bob2@example.com',
			role: 'user'
		});
		expect(onSuccess).toHaveBeenCalled();
	});

	it('includes password in the update payload when provided', async () => {
		updateUserMock.mockResolvedValueOnce(undefined);
		render(UserForm, { mode: 'edit', user: existingUser, onSuccess: vi.fn(), onCancel: vi.fn() });

		await fireEvent.input(screen.getByLabelText('New password (leave blank to keep)'), {
			target: { value: 'newpass1' }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));

		expect(updateUserMock).toHaveBeenCalledWith(5, {
			username: 'bob',
			email: 'bob@example.com',
			role: 'user',
			password: 'newpass1'
		});
	});

	it('shows an error message and does not call onSuccess when creation fails', async () => {
		createUserMock.mockRejectedValueOnce(new Error('conflict'));
		const onSuccess = vi.fn();
		render(UserForm, { mode: 'create', onSuccess, onCancel: vi.fn() });

		await fireEvent.input(screen.getByLabelText('Username'), { target: { value: 'dup' } });
		await fireEvent.input(screen.getByLabelText('Email'), { target: { value: 'dup@example.com' } });
		await fireEvent.input(screen.getByLabelText('Password'), { target: { value: 'hunter2' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Create user' }));

		expect(await screen.findByRole('alert')).toHaveTextContent(/could not create user/i);
		expect(onSuccess).not.toHaveBeenCalled();
	});

	it('calls onCancel when Cancel is clicked', async () => {
		const onCancel = vi.fn();
		render(UserForm, { mode: 'create', onSuccess: vi.fn(), onCancel });
		await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
		expect(onCancel).toHaveBeenCalled();
	});
});
