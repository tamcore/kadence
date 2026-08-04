import type { DarkVariant, ResolvedTheme, ThemePreference } from './types';

export const THEME_STORAGE_KEY = 'kadence_theme';
export const DARK_VARIANT_STORAGE_KEY = 'kadence_theme_dark';

export const DEFAULT_PREFERENCE: ThemePreference = 'auto';
export const DEFAULT_DARK_VARIANT: DarkVariant = 'dark';

export const THEME_PREFERENCES: readonly ThemePreference[] = ['auto', 'light', 'dark', 'amoled'];
export const DARK_VARIANTS: readonly DarkVariant[] = ['dark', 'amoled'];

export const DARK_MEDIA_QUERY = '(prefers-color-scheme: dark)';

/** Drives the mobile browser chrome and the PWA splash. Light keeps the brand navy. */
export const THEME_COLOR: Record<ResolvedTheme, string> = {
	light: '#021c46',
	dark: '#14171b',
	amoled: '#000000'
};

export const THEME_LABEL: Record<ThemePreference, string> = {
	auto: 'Auto',
	light: 'Light',
	dark: 'Dark',
	amoled: 'AMOLED'
};

// Deliberately NOT 'Dark'/'AMOLED': those names already belong to the outer
// radio group, and two radios with the same accessible name break
// document-wide getByRole lookups and confuse screen readers.
export const DARK_VARIANT_LABEL: Record<DarkVariant, string> = {
	dark: 'Soft dark',
	amoled: 'True black'
};
