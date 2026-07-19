import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';
import * as documentsApi from '$lib/api/documents';

describe('/documents', () => {
	beforeEach(() => vi.restoreAllMocks());

	it('loads and renders the user documents', async () => {
		vi.spyOn(documentsApi, 'listDocuments').mockResolvedValue([
			{ id: 1, filename: 'plan.pdf', mime: 'application/pdf', source_type: 'pdf', scope: 'private', created_at: '2026-07-19T10:00:00Z' }
		]);
		render(Page);
		await waitFor(() => expect(screen.getByText('plan.pdf')).toBeInTheDocument());
	});

	it('shows an error when loading fails', async () => {
		vi.spyOn(documentsApi, 'listDocuments').mockRejectedValue(new Error('boom'));
		render(Page);
		await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/could not load/i));
	});
});
