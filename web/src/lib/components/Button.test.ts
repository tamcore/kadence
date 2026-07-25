import { render, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { describe, expect, it } from 'vitest';
import Button from './Button.svelte';

function buttonLabel(text: string) {
	return createRawSnippet(() => ({ render: () => `<span>${text}</span>` }));
}

describe('Button', () => {
	it('keeps the default button at its intrinsic width', () => {
		render(Button, { children: buttonLabel('Continue') });

		expect(screen.getByRole('button', { name: 'Continue' })).not.toHaveClass('full-width');
	});

	it('fills its containing block when fullWidth is enabled', () => {
		render(Button, { children: buttonLabel('Continue'), fullWidth: true });

		expect(screen.getByRole('button', { name: 'Continue' })).toHaveClass('full-width');
	});
});
