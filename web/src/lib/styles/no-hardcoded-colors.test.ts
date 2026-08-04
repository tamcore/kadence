// @ts-expect-error Vitest runs in Node; app tsconfig intentionally omits Node types.
import { readFileSync, readdirSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

declare const process: { cwd(): string };

// A literal colour in a component <style> block cannot follow the theme. If
// something genuinely cannot be tokenised, add it here WITH a reason.
const ALLOWED: ReadonlyArray<{ file: string; declaration: string; why: string }> = [];

const LITERAL = /#[0-9a-fA-F]{3,8}\b|\brgba?\(|\bhsla?\(|:\s*(?:white|black)\b/;

function svelteFiles(dir: string): string[] {
	const entries: { name: string; isDirectory(): boolean; isFile(): boolean }[] = readdirSync(
		dir,
		{ withFileTypes: true }
	);
	return entries.flatMap((entry) => {
		const full = `${dir}/${entry.name}`;
		if (entry.isDirectory()) return svelteFiles(full);
		return entry.isFile() && entry.name.endsWith('.svelte') ? [full] : [];
	});
}

function styleBlocks(source: string): string[] {
	return [...source.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((m) => m[1]);
}

describe('no hardcoded colours in component styles', () => {
	it('routes every colour in every .svelte <style> block through a token', () => {
		const root = `${process.cwd()}/src`;
		const offenders: string[] = [];

		for (const file of svelteFiles(root)) {
			const relative = file.slice(root.length + 1);
			for (const block of styleBlocks(readFileSync(file, 'utf8'))) {
				for (const line of block.split('\n')) {
					const declaration = line.trim();
					if (!LITERAL.test(declaration)) continue;
					const allowed = ALLOWED.some(
						(a) => a.file === relative && a.declaration === declaration
					);
					if (!allowed) offenders.push(`${relative}: ${declaration}`);
				}
			}
		}

		expect(offenders).toEqual([]);
	});

	it('checks a meaningful number of files', () => {
		// Guards against the glob silently matching nothing.
		expect(svelteFiles(`${process.cwd()}/src`).length).toBeGreaterThan(30);
	});
});
