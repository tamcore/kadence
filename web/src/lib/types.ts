export interface User {
	id: number;
	username: string;
	email: string;
	role: 'admin' | 'user';
}

export interface Conversation {
	id: number;
	title: string;
	createdAt: string;
}

export interface ChatMessage {
	role: 'user' | 'assistant';
	content: string;
}

export type ChatEvent =
	| { type: 'meta'; conversationId: number }
	| { type: 'token'; delta: string }
	| { type: 'done' }
	| { type: 'error'; message: string };
