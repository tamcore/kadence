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
		expect(styles).toMatch(
			/\.sidebar\s*\{[\s\S]*?overflow-y:\s*auto;[\s\S]*?overscroll-behavior-y:\s*contain;/
		);
	});
});
