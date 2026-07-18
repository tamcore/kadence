import { marked } from 'marked';
import DOMPurify from 'isomorphic-dompurify';

marked.use({ gfm: true, breaks: true });

// renderMarkdown converts markdown to sanitized HTML (safe to use with {@html}).
export function renderMarkdown(md: string): string {
	const raw = marked.parse(md, { async: false }) as string;
	const wrapped = raw
		.replace(/<table(\s|>)/g, '<div class="table-scroll"><table$1')
		.replace(/<\/table>/g, '</table></div>');
	return DOMPurify.sanitize(wrapped);
}
