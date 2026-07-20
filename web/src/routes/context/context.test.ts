import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte';
import Page from './+page.svelte';
import * as contextApi from '$lib/api/context';
import type { KnowledgeOverview } from '$lib/api/context';

const baseOverview: KnowledgeOverview = {
	documentCount: 2,
	documentChunkCount: 10,
	conversationChunkCount: 5,
	documents: [{ id: 1, filename: 'plan.pdf', scope: 'private', createdAt: '2026-07-19T10:00:00Z' }],
	topTerms: [
		{ term: 'training', weight: 3.2, count: 8 },
		{ term: 'nutrition', weight: 1.1, count: 2 }
	]
};

describe('/context', () => {
	beforeEach(() => vi.restoreAllMocks());

	it('loads and renders the tag cloud terms', async () => {
		vi.spyOn(contextApi, 'getOverview').mockResolvedValue(baseOverview);
		render(Page);
		await waitFor(() => expect(screen.getByText('training')).toBeInTheDocument());
		expect(screen.getByText('nutrition')).toBeInTheDocument();
	});

	it('calls searchTerm and shows snippets when a term is clicked', async () => {
		vi.spyOn(contextApi, 'getOverview').mockResolvedValue(baseOverview);
		const searchSpy = vi.spyOn(contextApi, 'searchTerm').mockResolvedValue({
			term: 'training',
			snippets: [{ content: 'Weekly training plan details.', sourceKind: 'document', documentId: 1 }]
		});
		render(Page);
		const termButton = await screen.findByText('training');
		await fireEvent.click(termButton);
		expect(searchSpy).toHaveBeenCalledWith('training');
		await waitFor(() =>
			expect(screen.getByText('Weekly training plan details.')).toBeInTheDocument()
		);
	});

	it('shows the empty-state text when there are no top terms', async () => {
		vi.spyOn(contextApi, 'getOverview').mockResolvedValue({
			...baseOverview,
			topTerms: []
		});
		render(Page);
		await waitFor(() =>
			expect(screen.getByText(/no knowledge yet/i)).toBeInTheDocument()
		);
	});

	it('shows an error when loading fails', async () => {
		vi.spyOn(contextApi, 'getOverview').mockRejectedValue(new Error('boom'));
		render(Page);
		await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument());
	});
});
