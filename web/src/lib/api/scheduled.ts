import { api, getCsrfToken, handleUnauthorized, setCsrfToken } from '$lib/api/client';

const MAX_SCHEDULED_SSE_BYTES = 128 << 10;

export type ScheduledTaskState = 'draft' | 'active' | 'paused' | 'completed' | 'failed';
export type ScheduledRunState =
	| 'pending'
	| 'running'
	| 'no_change'
	| 'delivered'
	| 'completed'
	| 'failed';

export interface ScheduledQuestionOption {
	label: string;
	value: string;
}

export interface ScheduledQuestion {
	id: string;
	prompt: string;
	kind: 'single_select' | 'multi_select' | 'text';
	options?: ScheduledQuestionOption[];
	allowCustom: boolean;
	optional: boolean;
}

export interface ScheduledSchedule {
	At?: string;
	DTStart?: string;
	RRULE?: string;
	Timezone?: string;
	at?: string;
	dtStart?: string;
	rrule?: string;
	timezone?: string;
}

export interface ScheduledProposal {
	version: number;
	name: string;
	taskKind: 'reminder' | 'data' | 'monitoring';
	compiledPrompt: string;
	executionMode: 'static' | 'data';
	schedule: ScheduledSchedule;
	timezone: string;
	authorizedTools: string[];
	deliveryPolicy: 'always' | 'on_change';
	initialRun: 'wait' | 'preview' | 'baseline';
	stopCondition?: string;
	staticMessage?: string;
}

export interface ScheduledTask {
	id: string;
	conversationId: string;
	version: number;
	name: string;
	kind: 'reminder' | 'data' | 'monitoring';
	state: ScheduledTaskState;
	compiledPrompt: string;
	oneOffAt?: string;
	dtStart?: string;
	rrule?: string;
	timezone: string;
	executionMode: 'static' | 'data';
	authorizedTools: string[];
	deliveryPolicy: 'always' | 'on_change';
	initialRun: 'wait' | 'preview' | 'baseline';
	stopCondition?: string;
	staticMessage?: string;
	nextRunAt?: string;
	lastRunAt?: string;
	consecutiveFailures: number;
	unreadCount: number;
	recentRun?: ScheduledRun;
	createdAt: string;
	updatedAt: string;
}

export interface ScheduledRun {
	id: number;
	occurrenceKey: string;
	scheduledFor: string;
	state: ScheduledRunState;
	startedAt?: string;
	finishedAt?: string;
	result?: string;
	error?: string;
	unread: boolean;
	createdAt: string;
}

export type ScheduledDefinitionEvent =
	| { type: 'meta'; taskId: string; conversationId: string }
	| { type: 'text'; delta: string }
	| { type: 'task_question'; question: ScheduledQuestion }
	| { type: 'task_proposal'; proposal: ScheduledProposal }
	| { type: 'done' }
	| { type: 'error'; error: string; code?: number };

export interface ScheduledDefinitionRequest {
	taskId?: string;
	message: string;
}

function parseFrame(frame: string): {
	event: ScheduledDefinitionEvent | null;
	malformed: boolean;
} {
	const data = frame
		.split(/\r?\n/)
		.filter((line) => line.startsWith('data:'))
		.map((line) => line.slice(5).trimStart())
		.join('\n');
	if (!data) return { event: null, malformed: false };
	try {
		return { event: JSON.parse(data) as ScheduledDefinitionEvent, malformed: false };
	} catch {
		return { event: null, malformed: true };
	}
}

export async function* streamScheduledDefinition(
	request: ScheduledDefinitionRequest,
	signal: AbortSignal
): AsyncIterable<ScheduledDefinitionEvent> {
	const headers: Record<string, string> = { 'Content-Type': 'application/json' };
	const token = getCsrfToken();
	if (token) headers['X-CSRF-Token'] = token;
	const endpoint = request.taskId
		? `/api/scheduled/tasks/${encodeURIComponent(request.taskId)}/messages`
		: '/api/scheduled/tasks';
	const response = await fetch(endpoint, {
		method: 'POST',
		credentials: 'include',
		signal,
		headers,
		body: JSON.stringify({ message: request.message })
	});
	const rotated = response.headers.get('X-CSRF-Token');
	if (rotated) setCsrfToken(rotated);
	if (!response.ok || !response.body) {
		if (response.status === 401) {
			handleUnauthorized();
			yield { type: 'error', error: 'unauthorized', code: 401 };
		} else {
			yield { type: 'error', error: `scheduled request failed (${response.status})` };
		}
		return;
	}

	const reader = response.body.getReader();
	const decoder = new TextDecoder();
	let buffer = '';
	let bytes = 0;
	let completed = false;
	try {
		for (;;) {
			const { done, value } = await reader.read();
			if (done) break;
			bytes += value.byteLength;
			if (bytes > MAX_SCHEDULED_SSE_BYTES) {
				yield { type: 'error', error: 'scheduled response was too large' };
				return;
			}
			buffer += decoder.decode(value, { stream: true });
			const frames = buffer.split(/\r?\n\r?\n/);
			buffer = frames.pop() ?? '';
			for (const frame of frames) {
				const parsed = parseFrame(frame);
				if (parsed.malformed) {
					yield { type: 'error', error: 'scheduled response was malformed' };
					return;
				}
				if (parsed.event) {
					if (parsed.event.type === 'done') completed = true;
					yield parsed.event;
				}
			}
		}
		const tail = parseFrame(buffer + decoder.decode());
		if (tail.malformed) {
			yield { type: 'error', error: 'scheduled response was malformed' };
			return;
		}
		if (tail.event) {
			if (tail.event.type === 'done') completed = true;
			yield tail.event;
		}
		if (!completed) yield { type: 'error', error: 'scheduled response ended unexpectedly' };
	} finally {
		void reader.cancel().catch(() => undefined);
	}
}

export interface ScheduledList {
	tasks: ScheduledTask[];
	unreadCount: number;
	hasMore: boolean;
	nextOffset: number;
}

export interface ScheduledDetail {
	task: ScheduledTask;
	runs: ScheduledRun[];
	definitionMessages: ScheduledDefinitionMessage[];
}

export interface ScheduledDefinitionMessage {
	role: 'user' | 'assistant';
	text: string;
	question?: ScheduledQuestion;
}

export const listScheduledTasks = (offset = 0) =>
	api.get<ScheduledList>(`/scheduled/tasks?offset=${encodeURIComponent(offset)}`);
export const getScheduledTask = (id: string) =>
	api.get<ScheduledDetail>(`/scheduled/tasks/${encodeURIComponent(id)}`);
export const confirmScheduledTask = (id: string, expectedVersion: number) =>
	api.post<ScheduledTask>(`/scheduled/tasks/${encodeURIComponent(id)}/confirm`, {
		expectedVersion
	});
export const pauseScheduledTask = (id: string) =>
	api.patch<ScheduledTask>(`/scheduled/tasks/${encodeURIComponent(id)}`, { state: 'paused' });
export const resumeScheduledTask = (id: string) =>
	api.patch<ScheduledTask>(`/scheduled/tasks/${encodeURIComponent(id)}`, { state: 'active' });
export const runScheduledTaskNow = (id: string) =>
	api.post<ScheduledRun>(`/scheduled/tasks/${encodeURIComponent(id)}/run`, {});
export const markScheduledTaskRead = (id: string) =>
	api.post<{ ok: boolean }>(`/scheduled/tasks/${encodeURIComponent(id)}/read`, {});
export const deleteScheduledTask = (id: string) =>
	api.del<{ ok: boolean }>(`/scheduled/tasks/${encodeURIComponent(id)}`);
