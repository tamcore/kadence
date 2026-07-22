import { render } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import MarkdownMessage from './MarkdownMessage.svelte';

describe('MarkdownMessage', () => {
	it('renders GFM markdown as HTML', () => {
		const { container } = render(MarkdownMessage, { content: '# Hi\n\n**bold** text' });
		expect(container.querySelector('h1')).not.toBeNull();
		expect(container.querySelector('strong')?.textContent).toBe('bold');
	});

	it('strips a javascript: href from a markdown link', () => {
		const { container } = render(MarkdownMessage, { content: '[click me](javascript:alert(1))' });
		const link = container.querySelector('a');
		expect(link).not.toBeNull();
		expect(link?.getAttribute('href')).toBeNull();
		expect(container.innerHTML.toLowerCase()).not.toContain('javascript:');
	});

	it('strips a data: URI href from a markdown link', () => {
		const { container } = render(MarkdownMessage, {
			content: '[click me](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)'
		});
		const link = container.querySelector('a');
		expect(link).not.toBeNull();
		expect(link?.getAttribute('href')).toBeNull();
		expect(container.innerHTML.toLowerCase()).not.toContain('data:text/html');
	});

	it('sanitizes a raw <a> payload embedded in the message', () => {
		const { container } = render(MarkdownMessage, {
			content: '<a href="javascript:alert(1)" onclick="alert(2)">x</a>'
		});
		const link = container.querySelector('a');
		expect(link).not.toBeNull();
		expect(link?.getAttribute('href')).toBeNull();
		expect(link?.getAttribute('onclick')).toBeNull();
	});

	it('keeps a safe https href but does not add target/rel attributes', () => {
		const { container } = render(MarkdownMessage, { content: '[legit](https://example.com)' });
		const link = container.querySelector('a');
		expect(link?.getAttribute('href')).toBe('https://example.com');
		expect(link?.getAttribute('target')).toBeNull();
		expect(link?.getAttribute('rel')).toBeNull();
	});

	it('strips a script tag embedded in the message', () => {
		const { container } = render(MarkdownMessage, { content: '<script>alert(1)</script>hello' });
		expect(container.querySelector('script')).toBeNull();
		expect(container.textContent).toContain('hello');
	});

	it('wraps tables in a horizontally-scrollable container', () => {
		const { container } = render(MarkdownMessage, { content: '| a | b |\n|---|---|\n| 1 | 2 |' });
		expect(container.querySelector('.table-scroll table')).not.toBeNull();
	});
});
