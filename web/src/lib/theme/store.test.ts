import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { DARK_VARIANT_STORAGE_KEY, THEME_STORAGE_KEY } from './constants';

type StoreModule = typeof import('./store');

function stubMatchMedia(matches: boolean) {
	const listeners: Array<(e: MediaQueryListEvent) => void> = [];
	const removed: Array<(e: MediaQueryListEvent) => void> = [];
	Object.defineProperty(window, 'matchMedia', {
		configurable: true,
		writable: true,
		value: (query: string) => ({
			matches,
			media: query,
			addEventListener: (_: string, fn: (e: MediaQueryListEvent) => void) => listeners.push(fn),
			removeEventListener: (_: string, fn: (e: MediaQueryListEvent) => void) => removed.push(fn),
			addListener: () => {},
			removeListener: () => {},
			dispatchEvent: () => false
		})
	});
	return { listeners, removed };
}

async function freshStore(): Promise<StoreModule> {
	vi.resetModules();
	return import('./store');
}

beforeEach(() => {
	window.localStorage.clear();
	document.documentElement.removeAttribute('data-theme');
});

afterEach(() => vi.restoreAllMocks());

describe('theme stores', () => {
	it('defaults to auto and dark when storage is empty', async () => {
		stubMatchMedia(false);
		const { themePreference, darkVariant } = await freshStore();
		let pref: string | undefined;
		let variant: string | undefined;
		themePreference.subscribe((v) => (pref = v))();
		darkVariant.subscribe((v) => (variant = v))();
		expect(pref).toBe('auto');
		expect(variant).toBe('dark');
	});

	it('seeds from raw (unquoted) localStorage values', async () => {
		window.localStorage.setItem(THEME_STORAGE_KEY, 'amoled');
		window.localStorage.setItem(DARK_VARIANT_STORAGE_KEY, 'amoled');
		stubMatchMedia(false);
		const { themePreference, darkVariant } = await freshStore();
		let pref: string | undefined;
		let variant: string | undefined;
		themePreference.subscribe((v) => (pref = v))();
		darkVariant.subscribe((v) => (variant = v))();
		expect(pref).toBe('amoled');
		expect(variant).toBe('amoled');
	});

	it('ignores a JSON-quoted value, proving the raw-string contract', async () => {
		window.localStorage.setItem(THEME_STORAGE_KEY, '"dark"');
		stubMatchMedia(false);
		const { themePreference } = await freshStore();
		let pref: string | undefined;
		themePreference.subscribe((v) => (pref = v))();
		expect(pref).toBe('auto');
	});

	it('persists raw strings, not JSON', async () => {
		stubMatchMedia(false);
		const { setPreference, setDarkVariant } = await freshStore();
		setPreference('dark');
		setDarkVariant('amoled');
		expect(window.localStorage.getItem(THEME_STORAGE_KEY)).toBe('dark');
		expect(window.localStorage.getItem(DARK_VARIANT_STORAGE_KEY)).toBe('amoled');
	});

	it('resolves through the OS preference when set to auto', async () => {
		stubMatchMedia(true);
		const { resolvedTheme, setDarkVariant } = await freshStore();
		let resolved: string | undefined;
		const stop = resolvedTheme.subscribe((v) => (resolved = v));
		expect(resolved).toBe('dark');
		setDarkVariant('amoled');
		expect(resolved).toBe('amoled');
		stop();
	});

	it('applies the theme to the document on init', async () => {
		stubMatchMedia(true);
		const { initTheme } = await freshStore();
		const teardown = initTheme();
		expect(document.documentElement.dataset.theme).toBe('dark');
		teardown();
	});

	it('reacts to an OS change only while the preference is auto', async () => {
		const mq = stubMatchMedia(false);
		const { initTheme, setPreference } = await freshStore();
		const teardown = initTheme();
		expect(document.documentElement.dataset.theme).toBe('light');

		mq.listeners.forEach((fn) => fn({ matches: true } as MediaQueryListEvent));
		expect(document.documentElement.dataset.theme).toBe('dark');

		setPreference('light');
		// Flip systemPrefersDark twice (true -> false -> true) so both dispatches
		// are genuine changes under writable's safe_not_equal (a repeated `matches`
		// value is a no-op and would prove nothing recomputed). Ending back at
		// prefersDark=true is deliberate: a broken implementation that resolves
		// from systemPrefersDark regardless of the explicit preference would
		// produce 'dark' here (matching darkVariant), not 'light' — whereas
		// ending at prefersDark=false would coincidentally yield 'light' from
		// either the correct or the broken formula, proving nothing.
		mq.listeners.forEach((fn) => fn({ matches: false } as MediaQueryListEvent));
		mq.listeners.forEach((fn) => fn({ matches: true } as MediaQueryListEvent));
		expect(document.documentElement.dataset.theme).toBe('light');
		teardown();
	});

	it('removes the media listener on teardown', async () => {
		const mq = stubMatchMedia(false);
		const { initTheme } = await freshStore();
		initTheme()();
		expect(mq.removed).toHaveLength(1);
	});

	it('survives a missing matchMedia', async () => {
		Object.defineProperty(window, 'matchMedia', {
			configurable: true,
			writable: true,
			value: undefined
		});
		const { initTheme } = await freshStore();
		expect(() => initTheme()()).not.toThrow();
		expect(document.documentElement.dataset.theme).toBe('light');
	});
});
