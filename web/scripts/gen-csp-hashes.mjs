#!/usr/bin/env node
// Computes sha256 CSP hashes ("sha256-<base64>") for every inline <script>
// block in the built SPA's HTML, and writes them to build/csp-hashes.json.
//
// The Go server embeds this file alongside the rest of build/ (web/embed_prod.go)
// and reads it at startup (web/embed.go: CSPScriptHashes) to build a strict
// script-src CSP directive without inline 'unsafe-inline'. Runs as part of
// `npm run build` (package.json), so every build path that shells out to
// `npm run build` — Makefile build-prod/e2e-web, Dockerfile.dev, e2e.yaml,
// .goreleaser.yaml — produces this file automatically.
import { createHash } from 'node:crypto';
import { mkdir, readFile, readdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const webRoot = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const buildDir = path.join(webRoot, 'build');

// Matches inline <script> blocks (no src attribute) across an HTML document.
// SvelteKit's own bootstrap scripts are always inline (dynamic import() of
// hashed asset URLs); external chunks are loaded via <link rel="modulepreload">
// and <script type="module" src="..."> is not emitted by adapter-static's
// SPA fallback, but the src-attribute check below skips such tags anyway.
const INLINE_SCRIPT_RE = /<script(?![^>]*\ssrc=)[^>]*>([\s\S]*?)<\/script>/gi;

async function findHtmlFiles(dir) {
	const entries = await readdir(dir, { withFileTypes: true });
	const files = await Promise.all(
		entries.map(async (entry) => {
			const full = path.join(dir, entry.name);
			if (entry.isDirectory()) return findHtmlFiles(full);
			return entry.isFile() && entry.name.endsWith('.html') ? [full] : [];
		})
	);
	return files.flat();
}

function extractInlineScriptHashes(html) {
	const hashes = [];
	for (const match of html.matchAll(INLINE_SCRIPT_RE)) {
		const scriptBody = match[1];
		if (scriptBody.trim() === '') continue;
		const digest = createHash('sha256').update(scriptBody, 'utf8').digest('base64');
		hashes.push(`sha256-${digest}`);
	}
	return hashes;
}

async function main() {
	await mkdir(buildDir, { recursive: true });

	const htmlFiles = await findHtmlFiles(buildDir);
	const hashes = new Set();
	for (const file of htmlFiles) {
		const html = await readFile(file, 'utf8');
		for (const hash of extractInlineScriptHashes(html)) hashes.add(hash);
	}

	const sorted = [...hashes].sort();
	if (sorted.length === 0) {
		console.warn(
			'[gen-csp-hashes] no inline <script> hashes found under build/ — ' +
				'the server will fall back to its permissive dev CSP policy.'
		);
	}

	const outFile = path.join(buildDir, 'csp-hashes.json');
	await writeFile(outFile, `${JSON.stringify(sorted, null, 2)}\n`);
	console.log(`[gen-csp-hashes] wrote ${sorted.length} hash(es) to ${path.relative(webRoot, outFile)}`);
}

main().catch((err) => {
	console.error('[gen-csp-hashes] failed:', err);
	process.exitCode = 1;
});
