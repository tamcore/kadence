import { writable } from 'svelte/store';

export type UploadLifecycle = 'queued' | 'uploading' | 'processing' | 'done' | 'error';

export interface UploadProgressFile {
	ordinal: number;
	filename: string;
	state: UploadLifecycle;
	error?: string;
}

export interface UploadProgressBatch {
	id: number;
	files: UploadProgressFile[];
}

export const uploadBatch = writable<UploadProgressBatch | null>(null);

let nextBatchID = 1;
let activeBatch: UploadProgressBatch | null = null;
let closeTimer: ReturnType<typeof setTimeout> | undefined;

function publish(batch: UploadProgressBatch | null): void {
	activeBatch = batch;
	uploadBatch.set(batch);
}

function clearCloseTimer(): void {
	if (closeTimer) {
		clearTimeout(closeTimer);
		closeTimer = undefined;
	}
}

function allDone(batch: UploadProgressBatch): boolean {
	return batch.files.length > 0 && batch.files.every((file) => file.state === 'done');
}

function scheduleSuccessfulClose(batch: UploadProgressBatch): void {
	if (!allDone(batch) || closeTimer) return;
	closeTimer = setTimeout(() => {
		closeTimer = undefined;
		if (activeBatch?.id === batch.id && allDone(activeBatch)) publish(null);
	}, 500);
}

export function beginUploadBatch(files: readonly File[]): number {
	if (activeBatch) throw new Error('an upload batch is already active');
	const batch: UploadProgressBatch = {
		id: nextBatchID++,
		files: files.map((file, ordinal) => ({ ordinal, filename: file.name, state: 'queued' }))
	};
	publish(batch);
	return batch.id;
}

export function setUploadFileState(
	batchID: number,
	ordinal: number,
	state: UploadLifecycle,
	error?: string
): void {
	if (activeBatch?.id !== batchID) return;
	const previous = activeBatch;
	const index = previous.files.findIndex((file) => file.ordinal === ordinal);
	if (index === -1) return;
	if (previous.files[index].state === 'done' || previous.files[index].state === 'error') return;

	const files = previous.files.map((file) => {
		if (file.ordinal !== ordinal) return file;
		return state === 'error' && error
			? { ...file, state, error }
			: { ordinal: file.ordinal, filename: file.filename, state };
	});
	const next = { ...previous, files };
	publish(next);
	if (state === 'error' || !allDone(next)) clearCloseTimer();
	scheduleSuccessfulClose(next);
}

export function failUnsettledUploadFiles(batchID: number, message: string): void {
	if (activeBatch?.id !== batchID) return;
	const previous = activeBatch;
	if (!previous.files.some((file) => file.state !== 'done' && file.state !== 'error')) return;
	const files = previous.files.map((file) =>
		file.state === 'done' || file.state === 'error'
			? file
			: { ...file, state: 'error' as const, error: message }
	);
	publish({ ...previous, files });
	clearCloseTimer();
}

export function dismissUploadBatch(batchID: number): void {
	if (activeBatch?.id !== batchID) return;
	clearCloseTimer();
	publish(null);
}
