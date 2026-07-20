export interface User {
	id: number;
	username: string;
	email: string;
	role: 'admin' | 'user';
}

export interface Conversation {
	id: string;
	title: string;
	createdAt: string;
}

export type MessagePart =
	| { kind: 'text'; content: string }
	| { kind: 'tool'; tool: string; status: 'running' | 'done' | 'error'; arguments?: string };

export interface ChatMessage {
	role: 'user' | 'assistant';
	content: string;
	parts?: MessagePart[];
}

export type ChatEvent =
	| { type: 'meta'; conversationId: string }
	| { type: 'token'; delta: string }
	| { type: 'tool'; tool: string; status: 'running' | 'done' | 'error'; arguments?: string }
	| { type: 'done' }
	| { type: 'error'; message: string };

export interface Document {
	id: number;
	filename: string;
	mime: string;
	source_type: string;
	scope: 'private' | 'public';
	created_at: string;
}
