import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent, within } from '@testing-library/svelte';
import DocumentList from './DocumentList.svelte';
import type { Document } from '$lib/types';

const docs: Document[] = [
	{ id: 1, filename: 'plan.pdf', mime: 'application/pdf', source_type: 'pdf', scope: 'private', created_at: '2026-07-19T10:00:00Z' }
];

describe('DocumentList', () => {
	it('renders rows and fires ondelete', async () => {
		const ondelete = vi.fn();
		render(DocumentList, { documents: docs, ondelete });
		expect(screen.getByText('plan.pdf')).toBeInTheDocument();
		expect(screen.getByRole('table', { name: 'Documents' })).toBeInTheDocument();
		await fireEvent.click(screen.getByRole('button', { name: 'Delete plan.pdf' }));
		expect(ondelete).toHaveBeenCalledWith(1);
	});

	it('exposes labeled mobile card content and Delete through an action menu', async () => {
		const ondelete = vi.fn();
		render(DocumentList, { documents: docs, ondelete });

		const card = screen.getByRole('row', { name: 'plan.pdf' });
		expect(within(card).getByText('Filename')).toBeInTheDocument();
		expect(within(card).getByText('Type')).toBeInTheDocument();
		expect(within(card).getByText('Scope')).toBeInTheDocument();
		expect(within(card).getByText('Added')).toBeInTheDocument();

		await fireEvent.click(
			within(card).getByRole('button', { name: 'Actions for plan.pdf', hidden: true })
		);
		await fireEvent.click(screen.getByRole('menuitem', { name: 'Delete', hidden: true }));
		expect(ondelete).toHaveBeenCalledWith(1);
	});

	it('shows an empty state with no documents', () => {
		render(DocumentList, { documents: [], ondelete: vi.fn() });
		expect(screen.getByText(/no documents/i)).toBeInTheDocument();
	});
});
