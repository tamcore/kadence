import { fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

import Composer from './Composer.svelte';

afterEach(() => {
	vi.clearAllMocks();
});

describe('Composer', () => {
	it('calls onSubmit with trimmed text and clears the textarea on submit', async () => {
		const onSubmit = vi.fn();
		render(Composer, { props: { onSubmit } });

		const textarea = screen.getByRole('textbox') as HTMLTextAreaElement;
		await fireEvent.input(textarea, { target: { value: '  hello world  ' } });
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));

		expect(onSubmit).toHaveBeenCalledWith('hello world');
		expect(textarea.value).toBe('');
	});

	it('does not submit when disabled, even via click or Enter', async () => {
		const onSubmit = vi.fn();
		render(Composer, { props: { onSubmit, disabled: true } });

		const textarea = screen.getByRole('textbox') as HTMLTextAreaElement;
		await fireEvent.input(textarea, { target: { value: 'hello' } });
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));
		expect(onSubmit).not.toHaveBeenCalled();

		await fireEvent.keyDown(textarea, { key: 'Enter' });
		expect(onSubmit).not.toHaveBeenCalled();
	});

	it('submits on Enter without Shift', async () => {
		const onSubmit = vi.fn();
		render(Composer, { props: { onSubmit } });

		const textarea = screen.getByRole('textbox') as HTMLTextAreaElement;
		await fireEvent.input(textarea, { target: { value: 'hello' } });
		await fireEvent.keyDown(textarea, { key: 'Enter' });

		expect(onSubmit).toHaveBeenCalledWith('hello');
	});

	it('does not submit on Shift+Enter and does not prevent default', async () => {
		const onSubmit = vi.fn();
		render(Composer, { props: { onSubmit } });

		const textarea = screen.getByRole('textbox') as HTMLTextAreaElement;
		await fireEvent.input(textarea, { target: { value: 'hello' } });
		const notPrevented = await fireEvent.keyDown(textarea, { key: 'Enter', shiftKey: true });

		expect(onSubmit).not.toHaveBeenCalled();
		// fireEvent returns false if preventDefault() was called on the event
		expect(notPrevented).toBe(true);
	});

	it('does not submit empty or whitespace-only text', async () => {
		const onSubmit = vi.fn();
		render(Composer, { props: { onSubmit } });

		const textarea = screen.getByRole('textbox') as HTMLTextAreaElement;
		await fireEvent.input(textarea, { target: { value: '   ' } });
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));

		expect(onSubmit).not.toHaveBeenCalled();
	});
});
