import type { ScheduledProposal, ScheduledQuestion, ScheduledTaskState } from '$lib/api/scheduled';

export interface User {
	id: number;
	username: string;
	email: string;
	role: 'admin' | 'user';
	displayName: string;
	unitSystem: 'metric' | 'imperial';
	location: string;
	aboutMe: string;
	timezone: string;
	scheduledEnabled: boolean;
}

export interface Conversation {
	id: string;
	title: string;
	createdAt: string;
	lastActivityAt: string;
	pinnedAt: string | null;
}

export type MessagePart =
	| { kind: 'text'; content: string }
	| { kind: 'tool'; tool: string; status: 'running' | 'done' | 'error'; arguments?: string }
	| { kind: 'scheduled'; artifact: ScheduledArtifact };

export interface ScheduledArtifact {
	handoffId: string;
	taskId?: string;
	taskConversationId?: string;
	ordinal: number;
	artifactState: 'creating' | 'ready' | 'failed' | 'dismissed';
	taskState?: ScheduledTaskState;
	version?: number;
	question?: ScheduledQuestion;
	proposal?: ScheduledProposal;
	nextRunAt?: string;
	errorCode?: string;
	retryable?: boolean;
	reused?: boolean;
}

export interface ChatAttachment {
	id?: number;
	filename: string;
	mime: string;
	kind: 'image' | 'document';
	sizeBytes: number;
	imageWidth?: number;
	imageHeight?: number;
	ordinal: number;
}

export interface ChatDocumentReference {
	id?: number;
	documentId?: number;
	filename: string;
	scope: 'private' | 'public';
	ordinal: number;
	available: boolean;
}

export interface ChatMessage {
	id?: number;
	role: 'user' | 'assistant';
	content: string;
	parts?: MessagePart[];
	attachments?: ChatAttachment[];
	documentReferences?: ChatDocumentReference[];
	scheduledArtifacts?: ScheduledArtifact[];
	stopped?: boolean;
	purpose?: string;
}

export interface CredentialField {
	name: string;
	label?: string;
	secret?: boolean;
}

export interface CredentialRequest {
	requestId: string;
	reason: string;
	fields: CredentialField[];
}

export type ChatEvent =
	| {
			type: 'meta';
			conversationId: string;
			userMessageId?: number;
			attachments?: ChatAttachment[];
			documentReferences?: ChatDocumentReference[];
	  }
	| { type: 'title'; conversation: Conversation }
	| { type: 'token'; delta: string }
	| { type: 'tool'; tool: string; status: 'running' | 'done' | 'error'; arguments?: string }
	| { type: 'scheduled_artifact'; scheduledArtifact: ScheduledArtifact }
	| { type: 'credentials_request'; requestId: string; reason: string; fields: CredentialField[] }
	| { type: 'done'; assistantMessageId?: number; assistantContent?: string }
	| { type: 'error'; message: string; code?: number; assistantMessageId?: number; assistantContent?: string };

export interface Document {
	id: number;
	filename: string;
	mime: string;
	source_type: string;
	scope: 'private' | 'public';
	created_at: string;
}
