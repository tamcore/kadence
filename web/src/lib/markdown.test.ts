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
});
