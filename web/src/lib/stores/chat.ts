import { get, writable } from 'svelte/store';
import { goto } from '$app/navigation';
import { page } from '$app/stores';
import { APIError } from '$lib/api/client';
import type {
	ChatAttachment,
	ChatDocumentReference,
	ChatEvent,
	ChatMessage,
	Conversation,
	ConfirmRequest,
	CredentialRequest,
	Document,
	MessagePart,
	ScheduledArtifact
} from '$lib/types';
import * as chatApi from '$lib/api/chat';
import {
	beginUploadBatch,
	failUnsettledUploadFiles,
	setUploadFileState
} from '$lib/stores/upload-progress';

export const messages = writable<ChatMessage[]>([]);
export const conversations = writable<Conversation[]>([]);
export const activeId = writable<string | null>(null);
export const sending = writable(false);
export const messageActionPending = writable(false);
export const chatError = writable<string | null>(null);
export const credentialRequest = writable<CredentialRequest | null>(null);
export const confirmRequest = writable<ConfirmRequest | null>(null);
// conversationsRefreshError is set when a background/foreground refresh of the
// conversation list fails, so the sidebar can show an unobtrusive hint instead
// of silently leaving the list stale/empty.
export const conversationsRefreshError = writable(false);

let abort: AbortController | null = null;

export function newChat(): void {
	abort?.abort();
	abort = null;
	messages.set([]);
	activeId.set(null);
	chatError.set(null);
	sending.set(false);
	credentialRequest.set(null);
	confirmRequest.set(null);
}

// stopGeneration aborts the in-flight stream, if any. The partial assistant
// reply is kept and marked as stopped rather than discarded or surfaced as
// an error (see the abort branch in sendMessage's catch).
export function stopGeneration(): void {
	abort?.abort();
}

export async function refreshConversations(): Promise<void> {
	try {
		conversations.set(await chatApi.listConversations());
		conversationsRefreshError.set(false);
	} catch {
		// Non-fatal: keep whatever list is already shown, but let the sidebar
		// surface a hint rather than failing silently.
		conversationsRefreshError.set(true);
	}
}

export async function loadConversation(id: string): Promise<void> {
	// Already live/loaded — this covers the just-created conversation from the
	// home composer, whose in-flight stream must not be clobbered by a refetch.
	if (get(activeId) === id) return;
	activeId.set(id);
	chatError.set(null);
	try {
		messages.set((await chatApi.getMessages(id)).map(hydrateMessage));
	} catch {
		chatError.set('Could not load conversation');
	}
}

function sortArtifacts(artifacts: ScheduledArtifact[]): ScheduledArtifact[] {
	return [...artifacts].sort((a, b) => a.ordinal - b.ordinal);
}

const scheduledHandoffToolNames = new Set([
	'kadence__draft_future_unattended_task',
	'kadence__draft_scheduled_task'
]);

function textFirstParts(message: ChatMessage, artifacts: ScheduledArtifact[]): MessagePart[] {
	const transientTools = (message.parts ?? []).filter(
		(part): part is Extract<MessagePart, { kind: 'tool' }> => part.kind === 'tool'
	);
	return [
		{ kind: 'text', content: message.content },
		...transientTools,
		...sortArtifacts(artifacts).map((artifact) => ({ kind: 'scheduled' as const, artifact }))
	];
}

function hydrateMessage(message: ChatMessage): ChatMessage {
	if (message.role !== 'assistant') return message;
	const scheduledArtifacts = sortArtifacts(message.scheduledArtifacts ?? []);
	return {
		...message,
		...(scheduledArtifacts.length ? { scheduledArtifacts } : {}),
		parts: textFirstParts(message, scheduledArtifacts)
	};
}

// upsertScheduledPart keeps durable handoffs independent by ID. Their order is
// canonical (text first, then ordinal), even when a live stream included tools.
export function upsertScheduledPart(message: ChatMessage, artifact: ScheduledArtifact): ChatMessage {
	const existing = message.scheduledArtifacts ?? [];
	const index = existing.findIndex((item) => item.handoffId === artifact.handoffId);
	const scheduledArtifacts = sortArtifacts(
		index < 0
			? [...existing, artifact]
			: existing.map((item, itemIndex) => (itemIndex === index ? artifact : item))
	);
	return {
		...message,
		scheduledArtifacts,
		parts: textFirstParts(message, scheduledArtifacts)
	};
}

export async function removeConversation(id: string): Promise<void> {
	await chatApi.deleteConversation(id);
	if (get(activeId) === id) newChat();
	await refreshConversations();
}

function isViewingConversation(id: string): boolean {
	const currentPage = get(page);
	return currentPage.route.id === '/chat/[id]' && currentPage.params.id === id;
}

function clearDeletedConversation(id: string): void {
	if (get(activeId) !== id) return;
	const shouldNavigate = isViewingConversation(id);
	newChat();
	if (shouldNavigate) void goto('/chat');
}

export async function deleteUserMessage(messageId: number): Promise<void> {
	if (get(messageActionPending)) return;
	const conversationId = get(activeId);
	const current = get(messages);
	const userIdx = current.findIndex((message) => message.id === messageId && message.role === 'user');
	if (conversationId == null || userIdx < 0) {
		chatError.set('Could not delete message');
		return;
	}

	chatError.set(null);
	messageActionPending.set(true);
	try {
		const result = await chatApi.deleteMessage(conversationId, messageId);
		if (result.conversationDeleted) {
			clearDeletedConversation(conversationId);
		} else if (get(activeId) === conversationId) {
			messages.set(current.slice(0, userIdx));
		}
		await refreshConversations();
	} catch (error) {
		if (error instanceof APIError && error.status === 404) {
			try {
				const canonical = await chatApi.getMessages(conversationId);
				if (get(activeId) === conversationId) messages.set(canonical.map(hydrateMessage));
				chatError.set(null);
			} catch (reloadError) {
				if (reloadError instanceof APIError && reloadError.status === 404) {
					clearDeletedConversation(conversationId);
					await refreshConversations();
				} else {
					chatError.set(reloadError instanceof Error ? reloadError.message : 'Could not delete message');
				}
			}
		} else {
			chatError.set(error instanceof Error ? error.message : 'Could not delete message');
		}
	} finally {
		messageActionPending.set(false);
	}
}

export async function renameConversation(id: string, title: string): Promise<void> {
	await chatApi.renameConversation(id, title);
	await refreshConversations();
}

function compareConversationTie(a: Conversation, b: Conversation): number {
	return b.createdAt.localeCompare(a.createdAt) || b.id.localeCompare(a.id);
}

function sortConversations(items: Conversation[]): Conversation[] {
	const pinned = items
		.filter((item) => item.pinnedAt !== null)
		.sort((a, b) => b.pinnedAt!.localeCompare(a.pinnedAt!) || compareConversationTie(a, b));
	const recents = items
		.filter((item) => item.pinnedAt === null)
		.sort((a, b) => b.lastActivityAt.localeCompare(a.lastActivityAt) || compareConversationTie(a, b));
	return [...pinned, ...recents];
}

function upsertConversation(updated: Conversation): void {
	conversations.update((items) =>
		sortConversations(
			items.some((item) => item.id === updated.id)
				? items.map((item) => (item.id === updated.id ? updated : item))
				: [...items, updated]
		)
	);
}

// pinConversation waits for the canonical server response before changing the
// list. This deliberately avoids optimistic state so a failed request leaves
// the rendered Pinned/Recents partition exactly as it was.
export async function pinConversation(id: string, pinned: boolean): Promise<void> {
	const updated = await chatApi.pinConversation(id, pinned);
	conversations.update((items) => sortConversations(items.map((item) => (item.id === id ? updated : item))));
}

// appendTextDelta returns a copy of parts with delta appended to the trailing
// text part, adding a new text part first if the last part is a tool (or none exist).
function appendTextDelta(parts: MessagePart[], delta: string): MessagePart[] {
	const last = parts[parts.length - 1];
	if (last && last.kind === 'text') {
		const copy = [...parts];
		copy[copy.length - 1] = { kind: 'text', content: last.content + delta };
		return copy;
	}
	return [...parts, { kind: 'text', content: delta }];
}

// updateToolPart returns a copy of parts with the most recent running part for
// `tool` transitioned to `status`, or appends a new tool part if none is running.
function updateToolPart(
	parts: MessagePart[],
	tool: string,
	status: 'done' | 'error'
): MessagePart[] {
	for (let i = parts.length - 1; i >= 0; i--) {
		const part = parts[i];
		if (part.kind === 'tool' && part.tool === tool && part.status === 'running') {
			const copy = [...parts];
			copy[i] = { ...part, status };
			return copy;
		}
	}
	return [...parts, { kind: 'tool', tool, status }];
}

function optimisticAttachments(files: File[]): ChatAttachment[] {
	return files.map((file, ordinal) => ({
		filename: file.name,
		mime: file.type,
		kind: file.type.startsWith('image/') ? 'image' : 'document',
		sizeBytes: file.size,
		ordinal
	}));
}

function optimisticReferences(documents: Document[]): ChatDocumentReference[] {
	return documents.map((document, ordinal) => ({
		documentId: document.id,
		filename: document.filename,
		scope: document.scope,
		ordinal,
		available: true
	}));
}

// sendMessage streams a reply; returns the conversation id (new or existing), or null on error.
export async function sendMessage(
	text: string,
	files: File[] = [],
	documentReferences: Document[] = []
): Promise<string | null> {
	if (get(sending)) return null;
	chatError.set(null);
	credentialRequest.set(null);
	confirmRequest.set(null);
	sending.set(true);
	const restoreBeforeMeta = get(messages);
	const userIdx = restoreBeforeMeta.length;
	messages.update((m) => [
		...m,
		{
			role: 'user',
			content: text,
			...(files.length === 0 ? {} : { attachments: optimisticAttachments(files) }),
			...(documentReferences.length === 0
				? {}
				: { documentReferences: optimisticReferences(documentReferences) })
		}
	]);
	messages.update((m) => [...m, { role: 'assistant', content: '', parts: [] }]);
	const assistantIdx = get(messages).length - 1;
	const localAbort = beginStream();
	const uploadBatchID = files.length > 0 ? beginUploadBatch(files) : undefined;
	const body = {
		conversationId: get(activeId) ?? undefined,
		message: text,
		...(files.length === 0 ? {} : { files }),
		...(documentReferences.length === 0
			? {}
			: { documentIds: documentReferences.map((document) => document.id) })
	};
	if (uploadBatchID !== undefined) {
		files.forEach((_, ordinal) => setUploadFileState(uploadBatchID, ordinal, 'uploading'));
	}
	const convId = await consumeStream(
		chatApi.streamChat(body, localAbort.signal),
		userIdx,
		assistantIdx,
		get(activeId),
		localAbort,
		restoreBeforeMeta,
		false,
		uploadBatchID
	);
	if (convId != null) void refreshConversations();
	return convId;
}

// editMessage rewinds the local transcript at one persisted user message and
// streams its server-side replacement turn.
export async function editMessage(messageId: number, text: string): Promise<string | null> {
	if (get(sending)) return null;
	const conversationId = get(activeId);
	const current = get(messages);
	const userIdx = current.findIndex((message) => message.id === messageId && message.role === 'user');
	if (conversationId == null || userIdx < 0) {
		chatError.set('Could not edit message');
		return null;
	}

	chatError.set(null);
	credentialRequest.set(null);
	confirmRequest.set(null);
	sending.set(true);
	messages.set([
		...current.slice(0, userIdx),
		{ ...current[userIdx], content: text },
		{ role: 'assistant', content: '', parts: [] }
	]);
	const assistantIdx = userIdx + 1;
	const localAbort = beginStream();
	const convId = await consumeStream(
		chatApi.editMessage(conversationId, messageId, text, localAbort.signal),
		userIdx,
		assistantIdx,
		conversationId,
		localAbort,
		current,
		true
	);
	if (convId != null) void refreshConversations();
	return convId;
}

// regenerateMessage rewinds the local transcript before one persisted
// assistant response and streams its replacement.
export async function regenerateMessage(messageId: number): Promise<string | null> {
	if (get(sending)) return null;
	const conversationId = get(activeId);
	const current = get(messages);
	const assistantIdx = current.findIndex(
		(message) => message.id === messageId && message.role === 'assistant'
	);
	if (
		conversationId == null ||
		assistantIdx <= 0 ||
		current[assistantIdx - 1].role !== 'user'
	) {
		chatError.set('Could not regenerate response');
		return null;
	}

	chatError.set(null);
	credentialRequest.set(null);
	confirmRequest.set(null);
	sending.set(true);
	messages.set([
		...current.slice(0, assistantIdx),
		{ role: 'assistant', content: '', parts: [] }
	]);
	const localAbort = beginStream();
	const convId = await consumeStream(
		chatApi.regenerateMessage(conversationId, messageId, localAbort.signal),
		assistantIdx - 1,
		assistantIdx,
		conversationId,
		localAbort,
		current,
		true
	);
	if (convId != null) void refreshConversations();
	return convId;
}

function beginStream(): AbortController {
	const localAbort = new AbortController();
	abort = localAbort;
	return localAbort;
}

async function consumeStream(
	stream: AsyncIterable<ChatEvent>,
	userIdx: number,
	assistantIdx: number,
	initialConversationId: string | null,
	localAbort: AbortController,
	restoreBeforeMeta?: ChatMessage[],
	refetchAcceptedRewriteOnInterruption = false,
	uploadBatchID?: number
): Promise<string | null> {
	function updateAssistantParts(update: (parts: MessagePart[]) => MessagePart[]): void {
		messages.update((m) => {
			const copy = [...m];
			const current = copy[assistantIdx];
			if (!current) return m;
			const nextParts = update(current.parts ?? []);
			const textContent = nextParts
				.filter((p): p is Extract<MessagePart, { kind: 'text' }> => p.kind === 'text')
				.map((p) => p.content)
				.join('');
			const scheduledArtifacts = current.scheduledArtifacts ?? [];
			copy[assistantIdx] = {
				...current,
				role: 'assistant',
				content: textContent,
				parts: scheduledArtifacts.length
					? textFirstParts({ ...current, content: textContent, parts: nextParts }, scheduledArtifacts)
					: nextParts
			};
			return copy;
		});
	}
	function appendAssistantToken(delta: string): void {
		messages.update((current) => {
			const copy = [...current];
			const assistant = copy[assistantIdx];
			if (!assistant) return current;
			const content = assistant.content + delta;
			const scheduledArtifacts = assistant.scheduledArtifacts ?? [];
			copy[assistantIdx] = {
				...assistant,
				content,
				parts: scheduledArtifacts.length
					? textFirstParts({ ...assistant, content }, scheduledArtifacts)
					: appendTextDelta(assistant.parts ?? [], delta)
			};
			return copy;
		});
	}

	let convId = initialConversationId;
	let receivedMeta = false;
	function streamIsActive(): boolean {
		if (abort !== localAbort) return false;
		const streamConversationId = receivedMeta ? convId : initialConversationId;
		return get(activeId) === streamConversationId;
	}
	let receivedTerminal = false;
	let ownedStreamAtCleanup = false;
	function restoreRejectedRewrite(): void {
		if (
			!receivedMeta &&
			restoreBeforeMeta &&
			streamIsActive()
		) {
			messages.set(restoreBeforeMeta);
		}
	}
	async function refetchAcceptedRewrite(): Promise<void> {
		if (
			!refetchAcceptedRewriteOnInterruption ||
			!receivedMeta ||
			!restoreBeforeMeta ||
			initialConversationId === null
		)
			return;
		if (!streamIsActive()) return;
		try {
			const canonical = await chatApi.getMessages(initialConversationId);
			if (streamIsActive()) messages.set(canonical.map(hydrateMessage));
		} catch {
			// Keep the locally streamed rewrite when its canonical reload is unavailable.
		}
	}
	function applyPersistedAssistant(
		event: Extract<ChatEvent, { type: 'done' | 'error' }>
	): void {
		if (event.assistantMessageId === undefined && event.assistantContent === undefined) return;
		messages.update((current) => {
			const copy = [...current];
			const assistant = copy[assistantIdx];
			if (!assistant) return current;
			const content = event.assistantContent ?? assistant.content;
			const scheduledArtifacts = assistant.scheduledArtifacts ?? [];
			copy[assistantIdx] = {
				...assistant,
				...(event.assistantMessageId === undefined ? {} : { id: event.assistantMessageId }),
				content,
				parts: scheduledArtifacts.length
					? textFirstParts({ ...assistant, content }, scheduledArtifacts)
					: assistant.parts
			};
			return copy;
		});
	}
	try {
		for await (const ev of stream) {
			if (!streamIsActive()) return null;
			if (ev.type === 'upload') {
				if (uploadBatchID !== undefined) {
					if (ev.status === 'error') {
						setUploadFileState(uploadBatchID, ev.fileOrdinal, ev.status, ev.message);
					} else {
						setUploadFileState(uploadBatchID, ev.fileOrdinal, ev.status);
					}
				}
			} else if (ev.type === 'meta') {
				receivedMeta = true;
				convId = ev.conversationId;
				if (get(activeId) === null) activeId.set(convId);
				if (!streamIsActive()) return null;
				if (ev.userMessageId !== undefined) {
					messages.update((current) => {
						const copy = [...current];
						if (copy[userIdx]) {
							copy[userIdx] = {
								...copy[userIdx],
								id: ev.userMessageId,
								...(ev.attachments === undefined ? {} : { attachments: ev.attachments }),
								...(ev.documentReferences === undefined
									? {}
									: { documentReferences: ev.documentReferences })
							};
						}
						return copy;
					});
				}
			} else if (ev.type === 'title') {
				if (ev.conversation.id === convId) upsertConversation(ev.conversation);
			} else if (ev.type === 'token') {
				appendAssistantToken(ev.delta);
			} else if (ev.type === 'tool') {
				const tool = ev.tool;
				const status = ev.status;
				if (scheduledHandoffToolNames.has(tool)) continue;
				if (status === 'running') {
					updateAssistantParts((parts) => [
						...parts,
						{ kind: 'tool', tool, status: 'running', arguments: ev.arguments }
					]);
				} else {
					updateAssistantParts((parts) => updateToolPart(parts, tool, status));
				}
			} else if (ev.type === 'scheduled_artifact') {
				messages.update((current) => {
					const copy = [...current];
					const existingIdx = copy.findIndex((message) =>
						message.scheduledArtifacts?.some(
							(artifact) => artifact.handoffId === ev.scheduledArtifact.handoffId
						)
					);
					if (existingIdx >= 0) {
						copy[existingIdx] = upsertScheduledPart(copy[existingIdx], ev.scheduledArtifact);
						return copy;
					}
					const assistant = copy[assistantIdx];
					if (!assistant) return current;
					copy[assistantIdx] = upsertScheduledPart(assistant, ev.scheduledArtifact);
					return copy;
				});
			} else if (ev.type === 'credentials_request') {
				credentialRequest.set({
					requestId: ev.requestId,
					reason: ev.reason,
					fields: ev.fields
				});
			} else if (ev.type === 'confirm_request') {
				confirmRequest.set({
					requestId: ev.requestId,
					tool: ev.tool,
					message: ev.message
				});
			} else if (ev.type === 'error') {
				receivedTerminal = true;
				if (uploadBatchID !== undefined) {
					failUnsettledUploadFiles(uploadBatchID, ev.message);
				}
				applyPersistedAssistant(ev);
				restoreRejectedRewrite();
				await refetchAcceptedRewrite();
				if (streamIsActive()) {
					chatError.set(ev.message);
					credentialRequest.set(null);
					confirmRequest.set(null);
				}
				break;
			} else if (ev.type === 'done') {
				receivedTerminal = true;
				applyPersistedAssistant(ev);
				credentialRequest.set(null);
				confirmRequest.set(null);
				break;
			}
		}
		if (!receivedTerminal && uploadBatchID !== undefined) {
			failUnsettledUploadFiles(uploadBatchID, 'The chat stream was interrupted');
		}
		if (receivedMeta && !receivedTerminal && refetchAcceptedRewriteOnInterruption) {
			await refetchAcceptedRewrite();
			if (streamIsActive()) {
				chatError.set('The chat stream was interrupted');
				credentialRequest.set(null);
				confirmRequest.set(null);
			}
			return streamIsActive() ? convId : null;
		}
	} catch {
		// Intentional aborts should not surface as errors to the user; mark the
		// partial assistant reply as stopped instead so the UI can end cleanly.
		if (!localAbort.signal.aborted) {
			if (uploadBatchID !== undefined) {
				failUnsettledUploadFiles(uploadBatchID, 'The chat stream was interrupted');
			}
			restoreRejectedRewrite();
			await refetchAcceptedRewrite();
			if (streamIsActive()) {
				chatError.set('The chat stream was interrupted');
			}
		} else if (!receivedMeta && restoreBeforeMeta) {
			// Navigation/new-chat already replaced this conversation state.
			// Only restore an unaccepted rewrite when the same conversation
			// remains active (for example, Stop was pressed before meta).
			if (streamIsActive()) messages.set(restoreBeforeMeta);
		} else if (streamIsActive()) {
			messages.update((m) => {
				const copy = [...m];
				const current = copy[assistantIdx];
				if (current) copy[assistantIdx] = { ...current, stopped: true };
				return copy;
			});
			credentialRequest.set(null);
			confirmRequest.set(null);
		}
		return receivedMeta && streamIsActive() ? convId : null;
	} finally {
		// Only reset shared state if this send is still the active one
		ownedStreamAtCleanup = abort === localAbort;
		if (ownedStreamAtCleanup) {
			sending.set(false);
			abort = null;
		}
	}
	return receivedMeta && ownedStreamAtCleanup && get(activeId) === convId ? convId : null;
}
