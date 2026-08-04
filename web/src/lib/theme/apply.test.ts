import { afterEach, describe, expect, it } from 'vitest';
import { applyTheme } from './apply';
import { THEME_COLOR } from './constants';
import type { ResolvedTheme } from './types';

function meta(): HTMLMetaElement {
	let el = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]');
	if (!el) {
		el = document.createElement('meta');
		el.setAttribute('name', 'theme-color');
		document.head.appendChild(el);
	}
	return el;
}

afterEach(() => {
	document.documentElement.removeAttribute('data-theme');
	document.querySelector('meta[name="theme-color"]')?.remove();
});

describe('applyTheme', () => {
	it.each<ResolvedTheme>(['light', 'dark', 'amoled'])(
		'sets data-theme and theme-color for %s',
		(theme) => {
			const el = meta();
			applyTheme(theme);
			expect(document.documentElement.dataset.theme).toBe(theme);
			expect(el.getAttribute('content')).toBe(THEME_COLOR[theme]);
		}
	);

	it('still sets data-theme when the meta tag is absent', () => {
		expect(() => applyTheme('dark')).not.toThrow();
		expect(document.documentElement.dataset.theme).toBe('dark');
	});

	it('overwrites a previously applied theme', () => {
		meta();
		applyTheme('dark');
		applyTheme('light');
		expect(document.documentElement.dataset.theme).toBe('light');
		expect(document.querySelector('meta[name="theme-color"]')?.getAttribute('content')).toBe(
			THEME_COLOR.light
		);
	});
});
