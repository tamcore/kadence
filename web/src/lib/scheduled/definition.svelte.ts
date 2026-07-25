import { APIError } from '$lib/api/client';
import {
	confirmScheduledTask,
	getScheduledTask,
	streamScheduledDefinition,
	type ScheduledDefinitionMessage,
	type ScheduledProposal,
	type ScheduledQuestion,
	type ScheduledTask
} from '$lib/api/scheduled';

export interface ScheduledDefinitionTurn {
	role: 'user' | 'assistant';
	content: string;
}

export interface ScheduledQuestionHistoryEntry {
	question: ScheduledQuestion;
	answer?: string;
}

function definitionQuestionHistory(
	messages: ScheduledDefinitionMessage[]
): ScheduledQuestionHistoryEntry[] {
	const history: ScheduledQuestionHistoryEntry[] = [];
	let unanswered = -1;
	for (const message of messages) {
		if (message.role === 'assistant' && message.question) {
			history.push({ question: message.question });
			unanswered = history.length - 1;
		} else if (message.role === 'user' && unanswered >= 0) {
			history[unanswered] = { ...history[unanswered], answer: message.text };
			unanswered = -1;
		}
	}
	return history;
}

function proposalFromTask(task: ScheduledTask): ScheduledProposal {
	return {
		version: task.version,
		name: task.name,
		taskKind: task.kind,
		compiledPrompt: task.compiledPrompt,
		executionMode: task.executionMode,
		schedule: {
			At: task.oneOffAt,
			DTStart: task.dtStart,
			RRULE: task.rrule,
			Timezone: task.timezone
		},
		timezone: task.timezone,
		authorizedTools: task.authorizedTools ?? [],
		deliveryPolicy: task.deliveryPolicy,
		initialRun: task.initialRun,
		stopCondition: task.stopCondition,
		staticMessage: task.staticMessage
	};
}

export class ScheduledDefinitionController {
	taskId = $state<string | null>(null);
	task = $state<ScheduledTask | null>(null);
	turns = $state<ScheduledDefinitionTurn[]>([]);
	questionHistory = $state<ScheduledQuestionHistoryEntry[]>([]);
	questionIndex = $state(-1);
	question = $state<ScheduledQuestion | null>(null);
	proposal = $state<ScheduledProposal | null>(null);
	coachText = $state('');
	sending = $state(false);
	error = $state('');
	stale = $state(false);
	onUpdate: (() => void) | null = null;
	#request: AbortController | null = null;

	constructor(taskId: string | null) {
		this.taskId = taskId;
	}

	refine = async (message: string): Promise<void> => {
		if (this.sending) return;
		this.turns = [...this.turns, { role: 'user', content: message }];
		this.sending = true;
		this.error = '';
		this.stale = false;
		this.question = null;
		this.proposal = null;
		this.coachText = '';
		this.#request?.abort();
		this.#request = new AbortController();
		try {
			for await (const event of streamScheduledDefinition(
				{ taskId: this.taskId ?? undefined, message },
				this.#request.signal
			)) {
				switch (event.type) {
					case 'meta':
						this.taskId = event.taskId;
						this.notify();
						break;
					case 'text':
						this.coachText += event.delta;
						break;
					case 'task_question':
						this.recordQuestion(event.question);
						this.notify();
						break;
					case 'task_proposal':
						this.question = null;
						this.proposal = event.proposal;
						this.notify();
						break;
					case 'error':
						this.error = event.error;
						break;
				}
			}
			if (this.coachText) {
				this.turns = [...this.turns, { role: 'assistant', content: this.coachText }];
				this.coachText = '';
			}
		} catch (cause) {
			if (!(cause instanceof DOMException && cause.name === 'AbortError')) {
				this.error = cause instanceof Error ? cause.message : 'Could not refine this task';
			}
		} finally {
			this.sending = false;
		}
	};

	answerQuestion = async (answer: string): Promise<void> => {
		if (this.questionIndex >= 0) {
			this.questionHistory = this.questionHistory
				.slice(0, this.questionIndex + 1)
				.map((entry, index) => (index === this.questionIndex ? { ...entry, answer } : entry));
		}
		this.notify();
		await this.refine(answer);
	};

	showPreviousQuestion = (): void => {
		if (this.questionIndex <= 0) return;
		this.questionIndex -= 1;
		this.question = this.questionHistory[this.questionIndex].question;
		this.proposal = null;
		this.notify();
	};

	confirm = async (expectedVersion: number): Promise<ScheduledTask | null> => {
		if (!this.taskId || this.sending) return null;
		this.sending = true;
		this.error = '';
		try {
			const confirmed = await confirmScheduledTask(this.taskId, expectedVersion);
			this.task = confirmed;
			this.stale = false;
			return confirmed;
		} catch (cause) {
			if (cause instanceof APIError && cause.status === 409) {
				this.error = 'This plan changed while you were reviewing it. Refine it again to see the latest version.';
				this.stale = true;
				try {
					await this.loadCanonical();
				} catch {
					this.error =
						'This plan changed, and we could not reload the latest version. Please try again.';
				}
			} else {
				this.error = cause instanceof Error ? cause.message : 'Could not schedule this task';
			}
			return null;
		} finally {
			this.sending = false;
		}
	};

	reload = async (): Promise<void> => {
		if (!this.taskId) return;
		this.sending = true;
		this.error = '';
		try {
			await this.loadCanonical();
		} catch (cause) {
			this.error = cause instanceof Error ? cause.message : 'Could not resume this task';
		} finally {
			this.sending = false;
		}
	};

	reset = (): void => {
		this.#request?.abort();
		this.#request = null;
		this.taskId = null;
		this.task = null;
		this.turns = [];
		this.questionHistory = [];
		this.questionIndex = -1;
		this.question = null;
		this.proposal = null;
		this.coachText = '';
		this.sending = false;
		this.error = '';
		this.stale = false;
		this.notify();
	};

	dispose = (): void => {
		this.#request?.abort();
		this.#request = null;
	};

	private recordQuestion(nextQuestion: ScheduledQuestion): void {
		const current = this.questionHistory[this.questionIndex];
		if (current && current.question.id === nextQuestion.id && current.answer === undefined) {
			this.questionHistory[this.questionIndex] = { ...current, question: nextQuestion };
			this.questionHistory = [...this.questionHistory];
		} else {
			this.questionHistory = [...this.questionHistory, { question: nextQuestion }];
			this.questionIndex = this.questionHistory.length - 1;
		}
		this.question = nextQuestion;
	}

	private async loadCanonical(): Promise<void> {
		if (!this.taskId) return;
		const loaded = await getScheduledTask(this.taskId);
		this.task = loaded.task;
		this.turns = loaded.definitionMessages.map((message) => ({
			role: message.role,
			content: message.text
		}));
		this.questionHistory = definitionQuestionHistory(loaded.definitionMessages);
		this.questionIndex = this.questionHistory.length - 1;
		const latest = loaded.definitionMessages.at(-1);
		this.question = latest?.role === 'assistant' ? (latest.question ?? null) : null;
		this.proposal = loaded.task.compiledPrompt ? proposalFromTask(loaded.task) : null;
		this.notify();
	}

	private notify(): void {
		this.onUpdate?.();
	}
}
