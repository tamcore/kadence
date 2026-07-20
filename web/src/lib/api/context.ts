import { api } from '$lib/api/client';

export interface KnowledgeTerm {
	term: string;
	weight: number;
	count: number;
}

export interface KnowledgeDoc {
	id: number;
	filename: string;
	scope: string;
	createdAt: string;
}

export interface KnowledgeOverview {
	documentCount: number;
	documentChunkCount: number;
	conversationChunkCount: number;
	documents: KnowledgeDoc[];
	topTerms: KnowledgeTerm[];
}

export interface Snippet {
	content: string;
	sourceKind: string;
	documentId: number | null;
}

export interface SearchResult {
	term: string;
	snippets: Snippet[];
}

export function getOverview(): Promise<KnowledgeOverview> {
	return api.get<KnowledgeOverview>('/context/overview');
}

export function searchTerm(term: string): Promise<SearchResult> {
	return api.get<SearchResult>(`/context/search?term=${encodeURIComponent(term)}`);
}
