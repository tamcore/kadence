// @ts-expect-error Vitest runs in Node; app tsconfig intentionally omits Node types.
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { THEME_COLOR } from './constants';
import { resolveTheme } from './resolve';
import type { DarkVariant, ThemePreference } from './types';

declare const process: { cwd(): string };

const html = readFileSync(`${process.cwd()}/src/app.html`, 'utf8');

function bootstrapBody(): string {
	const match = html.match(/<script>([\s\S]*?)<\/script>/);
	if (!match) throw new Error('no inline bootstrap <script> found in app.html');
	return match[1];
}

function runBootstrap(pref: string | null, variant: string | null, prefersDark: boolean) {
	const root = { dataset: {} as Record<string, string> };
	const metaEl = {
		content: '',
		setAttribute(_name: string, value: string) {
			this.content = value;
		}
	};
	const store: Record<string, string> = {};
	if (pref !== null) store.kadence_theme = pref;
	if (variant !== null) store.kadence_theme_dark = variant;

	const fn = new Function('window', 'document', 'localStorage', bootstrapBody());
	fn(
		{ matchMedia: (query: string) => ({ matches: prefersDark, media: query }) },
		{ documentElement: root, querySelector: () => metaEl },
		{ getItem: (key: string) => store[key] ?? null }
	);
	return { theme: root.dataset.theme, themeColor: metaEl.content };
}

const PREFS: ThemePreference[] = ['auto', 'light', 'dark', 'amoled'];
const VARIANTS: DarkVariant[] = ['dark', 'amoled'];

describe('app.html theme bootstrap', () => {
	it('agrees with resolveTheme for all 16 combinations', () => {
		for (const pref of PREFS) {
			for (const variant of VARIANTS) {
				for (const prefersDark of [false, true]) {
					const got = runBootstrap(pref, variant, prefersDark);
					const want = resolveTheme(pref, variant, prefersDark);
					expect(got.theme, `${pref}/${variant}/prefersDark=${prefersDark}`).toBe(want);
					expect(got.themeColor).toBe(THEME_COLOR[want]);
				}
			}
		}
	});

	it('falls back to the auto default for empty or corrupt storage', () => {
		expect(runBootstrap(null, null, false).theme).toBe('light');
		expect(runBootstrap(null, null, true).theme).toBe('dark');
		expect(runBootstrap('nonsense', 'nonsense', true).theme).toBe('dark');
		expect(runBootstrap('DARK', null, false).theme).toBe('light');
	});

	it('runs before %sveltekit.head% so data-theme precedes the stylesheet', () => {
		expect(html.indexOf('<script>')).toBeLessThan(html.indexOf('%sveltekit.head%'));
	});

	it('keeps the theme-color meta ahead of the script that mutates it', () => {
		expect(html.indexOf('name="theme-color"')).toBeLessThan(html.indexOf('<script>'));
	});
});
