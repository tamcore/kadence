import { derived, writable, type Readable, type Writable } from 'svelte/store';
import { applyTheme } from './apply';
import { DARK_MEDIA_QUERY, DARK_VARIANT_STORAGE_KEY, THEME_STORAGE_KEY } from './constants';
import { asDarkVariant, asPreference, resolveTheme } from './resolve';
import type { DarkVariant, ResolvedTheme, ThemePreference } from './types';

// Values are stored as RAW strings, deliberately NOT via persisted<T>() from
// $lib/stores/auth — that helper JSON-stringifies, so the value on disk would be
// "dark" with quotes. The pre-paint bootstrap in app.html reads these keys before
// any bundle exists and must not need a JSON parser.
function readRaw(key: string): string | null {
	try {
		return typeof localStorage === 'undefined' ? null : localStorage.getItem(key);
	} catch {
		return null;
	}
}

function writeRaw(key: string, value: string): void {
	try {
		if (typeof localStorage !== 'undefined') localStorage.setItem(key, value);
	} catch {
		// Storage unavailable (private mode, quota). Stores stay in-memory.
	}
}

function systemDark(): boolean {
	if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false;
	return window.matchMedia(DARK_MEDIA_QUERY).matches;
}

export const themePreference: Writable<ThemePreference> = writable(
	asPreference(readRaw(THEME_STORAGE_KEY))
);
export const darkVariant: Writable<DarkVariant> = writable(
	asDarkVariant(readRaw(DARK_VARIANT_STORAGE_KEY))
);
export const systemPrefersDark: Writable<boolean> = writable(systemDark());

export const resolvedTheme: Readable<ResolvedTheme> = derived(
	[themePreference, darkVariant, systemPrefersDark],
	([pref, variant, prefersDark]) => resolveTheme(pref, variant, prefersDark)
);

export function setPreference(pref: ThemePreference): void {
	themePreference.set(pref);
	writeRaw(THEME_STORAGE_KEY, pref);
}

export function setDarkVariant(variant: DarkVariant): void {
	darkVariant.set(variant);
	writeRaw(DARK_VARIANT_STORAGE_KEY, variant);
}

/** Applies the theme and keeps it in sync with the OS. Returns a teardown. */
export function initTheme(): () => void {
	const stopApply = resolvedTheme.subscribe(applyTheme);
	if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return stopApply;

	const mq = window.matchMedia(DARK_MEDIA_QUERY);
	const onChange = (e: MediaQueryListEvent) => systemPrefersDark.set(e.matches);
	mq.addEventListener('change', onChange);
	return () => {
		mq.removeEventListener('change', onChange);
		stopApply();
	};
}
