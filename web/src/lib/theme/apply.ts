import { THEME_COLOR } from './constants';
import type { ResolvedTheme } from './types';

/**
 * A media-query'd <meta name="theme-color"> cannot be used here: it follows the
 * OS only and would ignore an explicit light/dark/amoled override.
 */
export function applyTheme(resolved: ResolvedTheme): void {
	if (typeof document === 'undefined') return;
	document.documentElement.dataset.theme = resolved;
	document
		.querySelector('meta[name="theme-color"]')
		?.setAttribute('content', THEME_COLOR[resolved]);
}
