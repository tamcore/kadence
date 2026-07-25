import { fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import source from './+page.svelte?raw';

const loginMock = vi.fn();
const getCurrentUserMock = vi.fn();
vi.mock('$lib/api/client', () => ({
	api: { login: (...a: unknown[]) => loginMock(...a), getCurrentUser: () => getCurrentUserMock() }
}));
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));

vi.mock('$lib/api/webauthn', () => ({
	isWebAuthnEnabled: vi.fn().mockResolvedValue(true),
	loginWithPasskey: vi.fn()
}));

import Login from './+page.svelte';

afterEach(() => vi.clearAllMocks());

describe('login page', () => {
	it('fills its assigned public viewport instead of creating a second viewport height', () => {
		expect(source).toMatch(/\.login\s*\{[\s\S]*?min-height:\s*100%;/);
	});

	it('shows decorative artwork without changing the Kadence heading name', () => {
		render(Login);

		const heading = screen.getByRole('heading', { name: 'Kadence' });
		const icon = heading.querySelector('img');
		expect(icon).toHaveAttribute('alt', '');
		expect(icon).toHaveAttribute('width', '72');
		expect(icon).toHaveAttribute('height', '72');
	});

	it('renders username + password fields and a submit button', () => {
		render(Login);
		expect(screen.getByLabelText(/username/i)).toBeInTheDocument();
		expect(screen.getByLabelText(/password/i)).toBeInTheDocument();
		expect(screen.getByRole('button', { name: /log in/i })).toBeInTheDocument();
	});

	it('renders both login actions at full width when passkeys are enabled', async () => {
		render(Login);

		expect(screen.getByRole('button', { name: 'Log in' })).toHaveClass('full-width');
		expect(await screen.findByRole('button', { name: '🔑 Sign in with a passkey' })).toHaveClass(
			'full-width'
		);
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
