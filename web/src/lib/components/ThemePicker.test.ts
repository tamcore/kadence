import { render, screen, fireEvent } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import ThemePicker from './ThemePicker.svelte';
import { DARK_VARIANT_STORAGE_KEY, THEME_STORAGE_KEY } from '$lib/theme/constants';
import { darkVariant, themePreference } from '$lib/theme/store';

beforeEach(() => {
	window.localStorage.clear();
	themePreference.set('auto');
	darkVariant.set('dark');
});

afterEach(() => {
	document.documentElement.removeAttribute('data-theme');
});

describe('ThemePicker', () => {
	it('offers all four appearances', () => {
		render(ThemePicker);
		for (const name of ['Auto', 'Light', 'Dark', 'AMOLED']) {
			expect(screen.getByRole('radio', { name })).toBeInTheDocument();
		}
	});

	it('marks the current preference as checked', () => {
		themePreference.set('amoled');
		render(ThemePicker);
		expect(screen.getByRole('radio', { name: 'AMOLED' })).toBeChecked();
		expect(screen.getByRole('radio', { name: 'Auto' })).not.toBeChecked();
	});

	it('shows the dark-variant sub-group only under auto', async () => {
		render(ThemePicker);
		expect(screen.getByRole('radio', { name: 'Soft dark' })).toBeInTheDocument();

		await fireEvent.click(screen.getByRole('radio', { name: 'Light' }));
		expect(screen.queryByRole('radio', { name: 'Soft dark' })).not.toBeInTheDocument();
	});

	it('applies instantly and persists a raw string, with no save button', async () => {
		render(ThemePicker);
		await fireEvent.click(screen.getByRole('radio', { name: 'Dark' }));
		expect(window.localStorage.getItem(THEME_STORAGE_KEY)).toBe('dark');
		expect(screen.queryByRole('button', { name: /save/i })).not.toBeInTheDocument();
	});

	it('persists the dark variant separately', async () => {
		render(ThemePicker);
		await fireEvent.click(screen.getByRole('radio', { name: 'True black' }));
		expect(window.localStorage.getItem(DARK_VARIANT_STORAGE_KEY)).toBe('amoled');
		expect(window.localStorage.getItem(THEME_STORAGE_KEY)).toBeNull();
	});

	it('does not reuse the unit-system radio labels', () => {
		render(ThemePicker);
		expect(screen.queryByRole('radio', { name: 'Metric' })).not.toBeInTheDocument();
		expect(screen.queryByRole('radio', { name: 'Imperial' })).not.toBeInTheDocument();
	});
});
