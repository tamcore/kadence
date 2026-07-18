import { fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

const loginMock = vi.fn();
const getCurrentUserMock = vi.fn();
vi.mock('$lib/api/client', () => ({
	api: { login: (...a: unknown[]) => loginMock(...a), getCurrentUser: () => getCurrentUserMock() }
}));
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));

import Login from './+page.svelte';

afterEach(() => vi.clearAllMocks());

describe('login page', () => {
	it('renders username + password fields and a submit button', () => {
		render(Login);
		expect(screen.getByLabelText(/username/i)).toBeInTheDocument();
		expect(screen.getByLabelText(/password/i)).toBeInTheDocument();
		expect(screen.getByRole('button', { name: /log in/i })).toBeInTheDocument();
	});

	it('shows an error message when login rejects', async () => {
		loginMock.mockRejectedValueOnce(new Error('bad'));
		render(Login);
		await fireEvent.input(screen.getByLabelText(/username/i), { target: { value: 'alice' } });
		await fireEvent.input(screen.getByLabelText(/password/i), { target: { value: 'wrong' } });
		await fireEvent.click(screen.getByRole('button', { name: /log in/i }));
		expect(await screen.findByRole('alert')).toHaveTextContent(/invalid/i);
	});
});
