// @ts-expect-error Vitest runs in Node; app tsconfig intentionally omits Node types.
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

declare const process: { cwd(): string };

const styles = readFileSync(`${process.cwd()}/src/lib/styles/app.css`, 'utf8');

describe('app mobile scroll containment', () => {
	it('locks the authenticated app to the dynamic viewport and contains its scrollers', () => {
		expect(styles).toMatch(
			/\.app-viewport\s*\{[\s\S]*?position:\s*fixed;[\s\S]*?inset:\s*0;[\s\S]*?overflow:\s*hidden;/
		);
		expect(styles).toMatch(
			/\.main\s*>\s*main\s*\{[\s\S]*?overflow-y:\s*auto;[\s\S]*?overscroll-behavior-y:\s*contain;/
		);
		expect(styles).toMatch(/\.sidebar\s*\{[\s\S]*?overflow:\s*hidden;/);
		expect(styles).toMatch(
			/\.sidebar-scroll\s*\{[\s\S]*?min-height:\s*0;[\s\S]*?overflow-y:\s*auto;[\s\S]*?overscroll-behavior-y:\s*contain;/
		);
	});

	it('disables mobile font boosting so wide scrolling tables keep a consistent size', () => {
		expect(styles).toMatch(
			/html,\s*body\s*\{[\s\S]*?text-size-adjust:\s*100%;/
		);
	});

	it('keeps the mobile brand link visually neutral', () => {
		expect(styles).toMatch(
			/\.brand-sm\s*\{[\s\S]*?color:\s*inherit;[\s\S]*?text-decoration:\s*none;/
		);
	});
});

const COLOUR_TOKENS = [
	'--bg', '--surface', '--surface-hover', '--text', '--text-muted', '--border',
	'--accent', '--accent-hover', '--on-accent', '--accent-tint',
	'--brand', '--on-brand',
	'--danger', '--success', '--warning', '--warning-bg', '--warning-fg',
	'--code-bg', '--overlay', '--shadow'
];

function block(selector: string): string {
	const i = styles.indexOf(selector);
	expect(i, `${selector} block is missing`).toBeGreaterThan(-1);
	const open = styles.indexOf('{', i);
	const close = styles.indexOf('}', open);
	return styles.slice(open, close);
}

describe('theme tokens', () => {
	it('defines every colour token in all three theme blocks', () => {
		for (const selector of [':root {', ":root[data-theme='dark']", ":root[data-theme='amoled']"]) {
			const body = block(selector);
			for (const token of COLOUR_TOKENS) {
				expect(body, `${selector} is missing ${token}`).toContain(`${token}:`);
			}
		}
	});

	it('declares color-scheme in all three theme blocks', () => {
		expect(block(':root {')).toMatch(/color-scheme:\s*light;/);
		expect(block(":root[data-theme='dark']")).toMatch(/color-scheme:\s*dark;/);
		expect(block(":root[data-theme='amoled']")).toMatch(/color-scheme:\s*dark;/);
	});

	it('uses true black for amoled and never for dark', () => {
		expect(block(":root[data-theme='amoled']")).toMatch(/--bg:\s*#000000;/);
		expect(block(":root[data-theme='dark']")).not.toMatch(/--bg:\s*#000000;/);
	});

	it('raises surface above bg in both dark themes', () => {
		// Slate ramp: bg #14171b < surface #1c2026; amoled #000000 < #0b0d10.
		expect(block(":root[data-theme='dark']")).toMatch(/--surface:\s*#1c2026;/);
		expect(block(":root[data-theme='amoled']")).toMatch(/--surface:\s*#0b0d10;/);
	});

	it('drops the dead --space token', () => {
		expect(styles).not.toContain('--space:');
	});

	it('routes the scrim and accent-button text through tokens', () => {
		expect(styles).toMatch(/\.scrim\s*\{[^}]*background:\s*var\(--overlay\);/);
		expect(styles).not.toMatch(/color:\s*#fff;/);
	});
});
