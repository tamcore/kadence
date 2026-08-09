import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import MessageActions from './MessageActions.svelte';

describe('MessageActions', () => {
	it('renders Delete after Edit and calls its callback', async () => {
		const onDelete = vi.fn();
		render(MessageActions, {
			props: { content: 'prompt', onEdit: vi.fn(), onDelete }
		});

		expect(screen.getAllByRole('button').map((button) => button.getAttribute('aria-label'))).toEqual([
			'Copy message',
			'Edit message',
			'Delete message'
		]);
		await fireEvent.click(screen.getByRole('button', { name: 'Delete message' }));
		expect(onDelete).toHaveBeenCalledOnce();
	});
});
