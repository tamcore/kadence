import { marked } from 'marked';
import DOMPurify from 'isomorphic-dompurify';

marked.use({ gfm: true, breaks: true });

// Anchor hardening: every <a> DOMPurify lets through (from markdown links or
// raw HTML in the message) gets target="_blank" and rel="noopener noreferrer"
// forced on, regardless of what the source specified. This closes the
// "tabnabbing" gap where a same-origin-looking link opened in a new tab could
// use window.opener to navigate the original tab to a phishing page.
DOMPurify.addHook('afterSanitizeAttributes', (node: Element) => {
	if (node.tagName === 'A' && node.hasAttribute('href')) {
		node.setAttribute('target', '_blank');
		node.setAttribute('rel', 'noopener noreferrer');
	}
});

// renderMarkdown converts markdown to sanitized HTML (safe to use with {@html}).
export function renderMarkdown(md: string): string {
	const raw = marked.parse(md, { async: false }) as string;
	const wrapped = raw
		.replace(/<table(\s|>)/g, '<div class="table-scroll"><table$1')
		.replace(/<\/table>/g, '</table></div>');
	return DOMPurify.sanitize(wrapped);
}
