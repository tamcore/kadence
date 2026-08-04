import { render, screen, fireEvent } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import ThemeCycleButton from './ThemeCycleButton.svelte';
import { THEME_STORAGE_KEY } from '$lib/theme/constants';
import { darkVariant, themePreference } from '$lib/theme/store';

beforeEach(() => {
	window.localStorage.clear();
	themePreference.set('auto');
	darkVariant.set('dark');
});

describe('ThemeCycleButton', () => {
	it('names the theme it will switch to', () => {
		render(ThemeCycleButton);
		expect(screen.getByRole('button', { name: 'Switch theme to Light' })).toBeInTheDocument();
	});

	it('cycles auto -> light -> dark -> amoled -> auto', async () => {
		render(ThemeCycleButton);
		const labels = ['Light', 'Dark', 'AMOLED', 'Auto'];
		const persisted = ['light', 'dark', 'amoled', 'auto'];
		for (let i = 0; i < labels.length; i++) {
			await fireEvent.click(screen.getByRole('button', { name: `Switch theme to ${labels[i]}` }));
			expect(window.localStorage.getItem(THEME_STORAGE_KEY)).toBe(persisted[i]);
		}
	});

	it('is a plain button, not a menu item', () => {
		render(ThemeCycleButton);
		expect(screen.queryByRole('menuitem')).not.toBeInTheDocument();
		expect(screen.queryByRole('separator')).not.toBeInTheDocument();
	});

	it('hides its icon from assistive tech', () => {
		const { container } = render(ThemeCycleButton);
		expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true');
	});
});
