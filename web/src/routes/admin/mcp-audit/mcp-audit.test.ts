import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import type { Writable } from 'svelte/store';
import * as auditApi from '$lib/api/mcpAudit';

const gotoMock = vi.fn();
vi.mock('$app/navigation', () => ({ goto: (...args: unknown[]) => gotoMock(...args) }));

const { isAdminStore } = vi.hoisted(() => {
	let value = false;
	const subscribers = new Set<(current: boolean) => void>();
	const store: Writable<boolean> = {
		subscribe(run: (current: boolean) => void) {
			run(value);
			subscribers.add(run);
			return () => subscribers.delete(run);
		},
		set(current: boolean) {
			value = current;
			subscribers.forEach((run) => run(value));
		},
		update(fn: (current: boolean) => boolean) {
			store.set(fn(value));
		}
	};
	return { isAdminStore: store };
});
vi.mock('$lib/stores/auth', () => ({ isAdmin: isAdminStore }));

import Page from './+page.svelte';

const call = {
	id: 42,
	actorUserId: 7,
	actorUsername: 'alice',
	conversationId: '11111111-1111-4111-8111-111111111111',
	source: 'chat' as const,
	model: 'coach-v2',
	toolCallId: 'call-42',
	toolName: 'garmin__activities',
	status: 'succeeded' as const,
	intent: '',
	guardVerdict: 'not_evaluated' as const,
	startedAt: '2026-07-25T10:00:00Z',
	finishedAt: '2026-07-25T10:00:01Z'
};

describe('/admin/mcp-audit', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
		gotoMock.mockClear();
		isAdminStore.set(false);
	});

	it('redirects non-admin users', async () => {
		render(Page);
		await waitFor(() => expect(gotoMock).toHaveBeenCalledWith('/'));
	});

	it('loads summaries, filters, and opens full detail', async () => {
		isAdminStore.set(true);
		const listSpy = vi.spyOn(auditApi, 'listMcpAuditCalls').mockResolvedValue({ items: [call] });
		const detailSpy = vi.spyOn(auditApi, 'getMcpAuditCall').mockResolvedValue({
			...call,
			arguments: '{"limit":5}',
			guardReason: '',
			result: '{"count":1}',
			error: ''
		});

		render(Page);

		await waitFor(() => expect(screen.getByText('garmin__activities')).toBeInTheDocument());
		expect(listSpy).toHaveBeenCalledWith(expect.objectContaining({ limit: 50 }));

		await fireEvent.input(screen.getByLabelText('Tool'), {
			target: { value: 'garmin__activities' }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Apply filters' }));
		await waitFor(() =>
			expect(listSpy).toHaveBeenLastCalledWith(
				expect.objectContaining({ tool: 'garmin__activities', limit: 50 })
			)
		);

		await fireEvent.click(screen.getByRole('button', { name: 'View call 42' }));
		await waitFor(() => expect(detailSpy).toHaveBeenCalledWith(42));
		expect(screen.getByText('{"limit":5}')).toBeInTheDocument();
		expect(screen.getByText('{"count":1}')).toBeInTheDocument();
	});

	it('renders an intent decision in the list and its reason only in the detail', async () => {
		isAdminStore.set(true);
		const deniedCall = {
			...call,
			status: 'blocked' as const,
			intent: 'Read weather',
			guardVerdict: 'denied' as const
		};
		vi.spyOn(auditApi, 'listMcpAuditCalls').mockResolvedValue({ items: [deniedCall] });
		vi.spyOn(auditApi, 'getMcpAuditCall').mockResolvedValue({
			...deniedCall,
			arguments: '{}',
			guardReason: 'Tool mismatch',
			result: '',
			error: ''
		});

		render(Page);

		await waitFor(() => expect(screen.getByText('Read weather')).toBeInTheDocument());
		expect(screen.getByText('denied')).toBeInTheDocument();
		expect(screen.queryByText('Tool mismatch')).not.toBeInTheDocument();
		await fireEvent.click(screen.getByRole('button', { name: 'View call 42' }));
		await waitFor(() => expect(screen.getByText('Tool mismatch')).toBeInTheDocument());
	});

	it('sends intent, verdict, and blocked status filters', async () => {
		isAdminStore.set(true);
		const listSpy = vi.spyOn(auditApi, 'listMcpAuditCalls').mockResolvedValue({ items: [] });
		render(Page);
		await waitFor(() => expect(listSpy).toHaveBeenCalledTimes(1));

		await fireEvent.input(screen.getByLabelText('Intent'), { target: { value: 'weather' } });
		await fireEvent.change(screen.getByLabelText('Verdict'), { target: { value: 'denied' } });
		await fireEvent.change(screen.getByLabelText('Status'), { target: { value: 'blocked' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Apply filters' }));

		await waitFor(() =>
			expect(listSpy).toHaveBeenLastCalledWith(
				expect.objectContaining({ intent: 'weather', guardVerdict: 'denied', status: 'blocked' })
			)
		);
	});

	it('exposes complete mobile summaries and opens detail through the action menu', async () => {
		isAdminStore.set(true);
		vi.spyOn(auditApi, 'listMcpAuditCalls').mockResolvedValue({ items: [call] });
		const detailSpy = vi.spyOn(auditApi, 'getMcpAuditCall').mockResolvedValue({
			...call,
			arguments: '{"limit":5}',
			guardReason: '',
			result: '{"count":1}',
			error: ''
		});
		render(Page);
		await waitFor(() => expect(screen.getByText('garmin__activities')).toBeInTheDocument());

		const card = screen.getByRole('row', { name: 'garmin__activities audit call' });
		for (const label of ['Status', 'Started', 'Tool', 'Source', 'Actor', 'Model', 'Duration']) {
			expect(within(card).getByText(label)).toBeInTheDocument();
		}

		await fireEvent.click(
			within(card).getByRole('button', { name: 'Actions for call 42', hidden: true })
		);
		await fireEvent.click(screen.getByRole('menuitem', { name: 'View', hidden: true }));
		await waitFor(() => expect(detailSpy).toHaveBeenCalledWith(42));
		expect(screen.getByLabelText('MCP call 42 detail')).toBeInTheDocument();
	});

	it('ignores a stale list response after filters change', async () => {
		isAdminStore.set(true);
		let resolveFirst!: (value: auditApi.McpAuditPage) => void;
		const first = new Promise<auditApi.McpAuditPage>((resolve) => {
			resolveFirst = resolve;
		});
		const listSpy = vi
			.spyOn(auditApi, 'listMcpAuditCalls')
			.mockImplementationOnce(() => first)
			.mockResolvedValueOnce({ items: [{ ...call, id: 43, toolName: 'fresh__tool' }] });

		render(Page);
		await fireEvent.input(screen.getByLabelText('Tool'), { target: { value: 'fresh__tool' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Apply filters' }));

		await waitFor(() => expect(listSpy).toHaveBeenCalledTimes(2));
		await waitFor(() => expect(screen.getByText('fresh__tool')).toBeInTheDocument());
		resolveFirst({ items: [{ ...call, toolName: 'stale__tool' }] });
		await new Promise((resolve) => setTimeout(resolve, 0));

		expect(screen.queryByText('stale__tool')).not.toBeInTheDocument();
		expect(screen.getByText('fresh__tool')).toBeInTheDocument();
	});

	it('does not reopen pending detail after filters change', async () => {
		isAdminStore.set(true);
		vi.spyOn(auditApi, 'listMcpAuditCalls')
			.mockResolvedValueOnce({ items: [call] })
			.mockResolvedValueOnce({ items: [] });
		let resolveDetail!: (value: auditApi.McpAuditDetail) => void;
		vi.spyOn(auditApi, 'getMcpAuditCall').mockImplementationOnce(
			() =>
				new Promise<auditApi.McpAuditDetail>((resolve) => {
					resolveDetail = resolve;
				})
		);

		render(Page);
		await waitFor(() => expect(screen.getByText('garmin__activities')).toBeInTheDocument());
		await fireEvent.click(screen.getByRole('button', { name: 'View call 42' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Apply filters' }));
		await waitFor(() => expect(screen.getByText('No calls match current filters.')).toBeInTheDocument());

		resolveDetail({ ...call, arguments: 'stale detail', guardReason: '', result: '', error: '' });
		await new Promise((resolve) => setTimeout(resolve, 0));

		expect(screen.queryByText('stale detail')).not.toBeInTheDocument();
	});
});
