import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
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
		await fireEvent.click(screen.getByRole('button', { name: /delete/i }));
		expect(ondelete).toHaveBeenCalledWith(1);
	});

	it('shows an empty state with no documents', () => {
		render(DocumentList, { documents: [], ondelete: vi.fn() });
		expect(screen.getByText(/no documents/i)).toBeInTheDocument();
	});
});
