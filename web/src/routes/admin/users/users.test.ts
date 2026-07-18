import { render, screen, waitFor } from '@testing-library/svelte';
import { readable } from 'svelte/store';
import { afterEach, describe, expect, it, vi } from 'vitest';

const listMock = vi.fn();
vi.mock('$lib/api/client', () => ({
	api: { listUsers: () => listMock(), createUser: vi.fn(), deleteUser: vi.fn() }
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
});
