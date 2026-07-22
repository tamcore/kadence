import { describe, expect, it } from 'vitest';
import { renderMarkdown } from './markdown';

describe('renderMarkdown', () => {
	it('renders GFM markdown to HTML', () => {
		const html = renderMarkdown('# Hi\n\n**bold** and `code`');
		expect(html).toContain('<h1');
		expect(html).toContain('<strong>bold</strong>');
		expect(html).toContain('<code>code</code>');
	});

	it('sanitizes dangerous HTML (XSS)', () => {
		const html = renderMarkdown('<img src=x onerror="alert(1)"> <script>alert(2)</script>');
		expect(html).not.toContain('onerror');
		expect(html.toLowerCase()).not.toContain('<script');
	});

	it('wraps tables in a scroll container', () => {
		const html = renderMarkdown('| a | b |\n|---|---|\n| 1 | 2 |');
		expect(html).toContain('table-scroll');
		expect(html).toContain('<table');
	});

	it('forces target="_blank" and rel="noopener noreferrer" on anchors', () => {
		const html = renderMarkdown('[legit](https://example.com)');
		expect(html).toContain('target="_blank"');
		expect(html).toContain('rel="noopener noreferrer"');
	});

	it('strips a javascript: href while still hardening the anchor', () => {
		const html = renderMarkdown('[bad](javascript:alert(1))');
		expect(html.toLowerCase()).not.toContain('javascript:');
	});

	it('strips a data: href while still hardening the anchor', () => {
		const html = renderMarkdown(
			'[bad](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)'
		);
		expect(html.toLowerCase()).not.toContain('data:text/html');
	});

	it('forces target/rel on a raw <a> payload embedded in the message', () => {
		const html = renderMarkdown('<a href="https://example.com" onclick="alert(1)">x</a>');
		expect(html).not.toContain('onclick');
		expect(html).toContain('target="_blank"');
		expect(html).toContain('rel="noopener noreferrer"');
	});
});
