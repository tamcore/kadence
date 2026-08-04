import {
	DARK_VARIANTS,
	DEFAULT_DARK_VARIANT,
	DEFAULT_PREFERENCE,
	THEME_PREFERENCES
} from './constants';
import type { DarkVariant, ResolvedTheme, ThemePreference } from './types';

/** localStorage is an untrusted boundary — anything can be in there. */
export function asPreference(raw: string | null): ThemePreference {
	return THEME_PREFERENCES.includes(raw as ThemePreference)
		? (raw as ThemePreference)
		: DEFAULT_PREFERENCE;
}

export function asDarkVariant(raw: string | null): DarkVariant {
	return DARK_VARIANTS.includes(raw as DarkVariant) ? (raw as DarkVariant) : DEFAULT_DARK_VARIANT;
}

export function resolveTheme(
	pref: ThemePreference,
	variant: DarkVariant,
	prefersDark: boolean
): ResolvedTheme {
	if (pref !== 'auto') return pref;
	return prefersDark ? variant : 'light';
}

export function nextPreference(current: ThemePreference): ThemePreference {
	const i = THEME_PREFERENCES.indexOf(current);
	return THEME_PREFERENCES[(i + 1) % THEME_PREFERENCES.length];
}
