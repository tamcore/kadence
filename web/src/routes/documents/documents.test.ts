import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent, within } from '@testing-library/svelte';
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

	it('asks for confirmation before deleting, and cancel keeps the document', async () => {
		vi.spyOn(documentsApi, 'listDocuments').mockResolvedValue([
			{ id: 1, filename: 'plan.pdf', mime: 'application/pdf', source_type: 'pdf', scope: 'private', created_at: '2026-07-19T10:00:00Z' }
		]);
		const deleteSpy = vi.spyOn(documentsApi, 'deleteDocument').mockResolvedValue(undefined);
		render(Page);
		await waitFor(() => expect(screen.getByText('plan.pdf')).toBeInTheDocument());

		await fireEvent.click(screen.getByRole('button', { name: /delete/i }));
		const dialog = await screen.findByRole('dialog', { name: 'Delete document' });
		expect(within(dialog).getByText(/plan\.pdf/)).toBeInTheDocument();

		await fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
		expect(deleteSpy).not.toHaveBeenCalled();
		expect(screen.getByText('plan.pdf')).toBeInTheDocument();
	});

	it('deletes the document once confirmed', async () => {
		vi.spyOn(documentsApi, 'listDocuments')
			.mockResolvedValueOnce([
				{ id: 1, filename: 'plan.pdf', mime: 'application/pdf', source_type: 'pdf', scope: 'private', created_at: '2026-07-19T10:00:00Z' }
			])
			.mockResolvedValueOnce([]);
		const deleteSpy = vi.spyOn(documentsApi, 'deleteDocument').mockResolvedValue(undefined);
		render(Page);
		await waitFor(() => expect(screen.getByText('plan.pdf')).toBeInTheDocument());

		await fireEvent.click(screen.getByRole('button', { name: /delete/i }));
		const dialog = await screen.findByRole('dialog', { name: 'Delete document' });
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));

		await waitFor(() => expect(deleteSpy).toHaveBeenCalledWith(1));
	});
});
