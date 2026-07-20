import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import { get } from 'svelte/store';
import CredentialPrompt from './CredentialPrompt.svelte';
import * as credentialsApi from '$lib/api/credentials';
import { credentialRequest } from '$lib/stores/chat';
import type { CredentialRequest } from '$lib/types';

const sampleRequest: CredentialRequest = {
	requestId: 'req-1',
	reason: 'Garmin login required to fetch your activities.',
	fields: [
		{ name: 'username', label: 'Username' },
		{ name: 'password', label: 'Password', secret: true }
	]
};

describe('CredentialPrompt', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
		credentialRequest.set(sampleRequest);
	});

	it('renders the reason and one input per field with correct types', () => {
		render(CredentialPrompt, { request: sampleRequest });

		expect(screen.getByText(sampleRequest.reason)).toBeInTheDocument();

		const usernameInput = screen.getByLabelText('Username') as HTMLInputElement;
		expect(usernameInput.type).toBe('text');
		expect(usernameInput.autocomplete).toBe('off');

		const passwordInput = screen.getByLabelText('Password') as HTMLInputElement;
		expect(passwordInput.type).toBe('password');
		expect(passwordInput.autocomplete).toBe('off');
	});

	it('submits entered values and clears the store on success', async () => {
		const spy = vi.spyOn(credentialsApi, 'submitCredentials').mockResolvedValue(undefined);
		render(CredentialPrompt, { request: sampleRequest });

		await fireEvent.input(screen.getByLabelText('Username'), { target: { value: 'alice' } });
		await fireEvent.input(screen.getByLabelText('Password'), { target: { value: 'hunter2' } });
		await fireEvent.click(screen.getByRole('button', { name: /submit/i }));

		await waitFor(() =>
			expect(spy).toHaveBeenCalledWith('req-1', { username: 'alice', password: 'hunter2' })
		);
		await waitFor(() => expect(get(credentialRequest)).toBeNull());
	});

	it('shows an error line when submission fails and does not clear the store', async () => {
		vi.spyOn(credentialsApi, 'submitCredentials').mockRejectedValue(new Error('boom'));
		render(CredentialPrompt, { request: sampleRequest });

		await fireEvent.click(screen.getByRole('button', { name: /submit/i }));

		await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument());
		expect(get(credentialRequest)).toEqual(sampleRequest);
	});

	it('clears the store on cancel without submitting', async () => {
		const spy = vi.spyOn(credentialsApi, 'submitCredentials');
		render(CredentialPrompt, { request: sampleRequest });

		await fireEvent.click(screen.getByRole('button', { name: /cancel/i }));

		expect(spy).not.toHaveBeenCalled();
		expect(get(credentialRequest)).toBeNull();
	});
});
