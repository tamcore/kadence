import { get } from 'svelte/store';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
	beginUploadBatch,
	dismissUploadBatch,
	failUnsettledUploadFiles,
	setUploadFileState,
	uploadBatch
} from './upload-progress';

function files(...names: string[]): File[] {
	return names.map((name) => new File(['content'], name));
}

afterEach(() => {
	vi.useRealTimers();
	const batch = get(uploadBatch);
	if (batch) dismissUploadBatch(batch.id);
});

describe('upload progress store', () => {
	it('creates ordered queued rows without retaining mutable caller data', () => {
		const selected = files('first.pdf', 'second.png');
		const id = beginUploadBatch(selected);
		const firstSnapshot = get(uploadBatch);

		selected.reverse();

		expect(firstSnapshot).toEqual({
			id,
			files: [
				{ ordinal: 0, filename: 'first.pdf', state: 'queued' },
				{ ordinal: 1, filename: 'second.png', state: 'queued' }
			]
		});
	});

	it('replaces changed rows instead of mutating a published batch', () => {
		const id = beginUploadBatch(files('plan.pdf'));
		const before = get(uploadBatch);

		setUploadFileState(id, 0, 'uploading');

		expect(before).toEqual({ id, files: [{ ordinal: 0, filename: 'plan.pdf', state: 'queued' }] });
		expect(get(uploadBatch)).toEqual({ id, files: [{ ordinal: 0, filename: 'plan.pdf', state: 'uploading' }] });
		expect(get(uploadBatch)).not.toBe(before);
		expect(get(uploadBatch)?.files).not.toBe(before?.files);
	});

	it('rejects a new batch while another batch is active', () => {
		beginUploadBatch(files('plan.pdf'));

		expect(() => beginUploadBatch(files('notes.txt'))).toThrow('an upload batch is already active');
	});

	it('ignores lifecycle events for a stale batch ID', () => {
		const staleID = beginUploadBatch(files('old.pdf'));
		dismissUploadBatch(staleID);
		const currentID = beginUploadBatch(files('current.pdf'));

		setUploadFileState(staleID, 0, 'error', 'Old request failed');
		failUnsettledUploadFiles(staleID, 'Old request failed');
		dismissUploadBatch(staleID);

		expect(get(uploadBatch)).toEqual({
			id: currentID,
			files: [{ ordinal: 0, filename: 'current.pdf', state: 'queued' }]
		});
	});

	it('fails only files that are not already settled', () => {
		const id = beginUploadBatch(files('complete.pdf', 'waiting.png', 'active.txt'));
		setUploadFileState(id, 0, 'done');
		setUploadFileState(id, 2, 'uploading');

		failUnsettledUploadFiles(id, 'Connection lost');

		expect(get(uploadBatch)?.files).toEqual([
			{ ordinal: 0, filename: 'complete.pdf', state: 'done' },
			{ ordinal: 1, filename: 'waiting.png', state: 'error', error: 'Connection lost' },
			{ ordinal: 2, filename: 'active.txt', state: 'error', error: 'Connection lost' }
	]);
	});

	it('does not mutate settled rows after late lifecycle events', () => {
		const id = beginUploadBatch(files('failed.pdf', 'complete.png'));
		setUploadFileState(id, 0, 'error', 'Connection lost');
		setUploadFileState(id, 1, 'done');
		const settled = get(uploadBatch);

		setUploadFileState(id, 0, 'done');
		setUploadFileState(id, 1, 'error', 'Late failure');
		setUploadFileState(id, 1, 'processing');

		expect(get(uploadBatch)).toBe(settled);
		expect(get(uploadBatch)?.files).toEqual([
			{ ordinal: 0, filename: 'failed.pdf', state: 'error', error: 'Connection lost' },
			{ ordinal: 1, filename: 'complete.png', state: 'done' }
		]);
	});

	it('closes a successful batch 500 ms after every file is done', () => {
		vi.useFakeTimers();
		const id = beginUploadBatch(files('first.pdf', 'second.pdf'));
		setUploadFileState(id, 0, 'done');
		vi.advanceTimersByTime(500);
		expect(get(uploadBatch)).not.toBeNull();

		setUploadFileState(id, 1, 'done');
		vi.advanceTimersByTime(499);
		expect(get(uploadBatch)).not.toBeNull();
		vi.advanceTimersByTime(1);
		expect(get(uploadBatch)).toBeNull();
	});

	it('keeps an errored batch open until it is dismissed', () => {
		vi.useFakeTimers();
		const id = beginUploadBatch(files('plan.pdf'));
		setUploadFileState(id, 0, 'error', 'Upload failed');
		vi.advanceTimersByTime(500);

		expect(get(uploadBatch)).toEqual({
			id,
			files: [{ ordinal: 0, filename: 'plan.pdf', state: 'error', error: 'Upload failed' }]
		});

		dismissUploadBatch(id);
		expect(get(uploadBatch)).toBeNull();
	});
});
