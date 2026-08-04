import { describe, expect, it } from 'vitest';
import { asDarkVariant, asPreference, nextPreference, resolveTheme } from './resolve';
import { DARK_VARIANTS, THEME_PREFERENCES } from './constants';
import type { DarkVariant, ResolvedTheme, ThemePreference } from './types';

type Row = [ThemePreference, DarkVariant, boolean, ResolvedTheme];

// 4 preferences x 2 variants x 2 OS states. The variant is only consulted
// when the preference is 'auto' AND the OS asks for dark.
const TABLE: Row[] = [
	['auto', 'dark', false, 'light'],
	['auto', 'dark', true, 'dark'],
	['auto', 'amoled', false, 'light'],
	['auto', 'amoled', true, 'amoled'],
	['light', 'dark', false, 'light'],
	['light', 'dark', true, 'light'],
	['light', 'amoled', false, 'light'],
	['light', 'amoled', true, 'light'],
	['dark', 'dark', false, 'dark'],
	['dark', 'dark', true, 'dark'],
	['dark', 'amoled', false, 'dark'],
	['dark', 'amoled', true, 'dark'],
	['amoled', 'dark', false, 'amoled'],
	['amoled', 'dark', true, 'amoled'],
	['amoled', 'amoled', false, 'amoled'],
	['amoled', 'amoled', true, 'amoled']
];

describe('resolveTheme', () => {
	it.each(TABLE)('%s + %s variant + prefersDark=%s -> %s', (pref, variant, dark, want) => {
		expect(resolveTheme(pref, variant, dark)).toBe(want);
	});

	it('covers every preference and variant combination', () => {
		expect(TABLE).toHaveLength(THEME_PREFERENCES.length * DARK_VARIANTS.length * 2);
	});
});

describe('asPreference', () => {
	it('accepts every known preference', () => {
		for (const pref of THEME_PREFERENCES) expect(asPreference(pref)).toBe(pref);
	});

	it.each([null, '', 'DARK', 'nonsense', 'Auto', 'amoled '])('coerces %j to auto', (raw) => {
		expect(asPreference(raw as string | null)).toBe('auto');
	});
});

describe('asDarkVariant', () => {
	it('accepts every known variant', () => {
		for (const v of DARK_VARIANTS) expect(asDarkVariant(v)).toBe(v);
	});

	it.each([null, '', 'auto', 'light', 'AMOLED'])('coerces %j to dark', (raw) => {
		expect(asDarkVariant(raw as string | null)).toBe('dark');
	});
});

describe('nextPreference', () => {
	it('cycles auto -> light -> dark -> amoled -> auto', () => {
		expect(nextPreference('auto')).toBe('light');
		expect(nextPreference('light')).toBe('dark');
		expect(nextPreference('dark')).toBe('amoled');
		expect(nextPreference('amoled')).toBe('auto');
	});

	it('returns to the start after a full lap', () => {
		let pref: ThemePreference = 'auto';
		for (let i = 0; i < THEME_PREFERENCES.length; i++) pref = nextPreference(pref);
		expect(pref).toBe('auto');
	});
});
