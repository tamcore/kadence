import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor, within } from '@testing-library/svelte';
import Page from './+page.svelte';
import * as mcpApi from '$lib/api/mcp';
import type { McpServer } from '$lib/api/mcp';

const ownServer: McpServer = {
	id: 7,
	name: 'my-server',
	transport: 'streamable-http',
	scope: 'user',
	state: 'healthy',
	toolCount: 3,
	url: 'https://example.test/mcp',
	editable: true
};

const globalServer: McpServer = {
	name: 'shared-server',
	transport: 'sse',
	scope: 'global',
	state: 'healthy',
	toolCount: 5,
	editable: false
};

describe('/mcp', () => {
	beforeEach(() => vi.restoreAllMocks());

	it('shows an error when loading fails', async () => {
		vi.spyOn(mcpApi, 'listMcp').mockRejectedValue(new Error('boom'));
		render(Page);
		await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('boom'));
	});

	it('renders servers and only shows Edit/Delete for editable ones', async () => {
		vi.spyOn(mcpApi, 'listMcp').mockResolvedValue({
			servers: [ownServer, globalServer],
			canAdd: true
		});
		render(Page);

		await waitFor(() => expect(screen.getByText('my-server')).toBeInTheDocument());
		expect(screen.getByText('shared-server')).toBeInTheDocument();
		expect(screen.getAllByRole('button', { name: 'Edit' })).toHaveLength(1);
		expect(screen.getAllByRole('button', { name: 'Delete' })).toHaveLength(1);
	});

	it('shows the add form only when canAdd is true, and creates a server on submit', async () => {
		vi.spyOn(mcpApi, 'listMcp').mockResolvedValue({ servers: [], canAdd: true });
		const createSpy = vi.spyOn(mcpApi, 'createMcp').mockResolvedValue(undefined);
		render(Page);

		await waitFor(() => expect(screen.getByRole('button', { name: 'Add MCP server' })).toBeInTheDocument());
		await fireEvent.click(screen.getByRole('button', { name: 'Add MCP server' }));

		await fireEvent.input(screen.getByPlaceholderText('name'), { target: { value: 'new-one' } });
		await fireEvent.input(screen.getByPlaceholderText('https://…'), {
			target: { value: 'https://new.test/mcp' }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Add server' }));

		await waitFor(() =>
			expect(createSpy).toHaveBeenCalledWith({
				name: 'new-one',
				url: 'https://new.test/mcp',
				transport: 'streamable-http',
				authUser: '',
				authPass: '',
				alias: '',
				hint: ''
			})
		);
	});

	it('submits the alias and hint fields when provided', async () => {
		vi.spyOn(mcpApi, 'listMcp').mockResolvedValue({ servers: [], canAdd: true });
		const createSpy = vi.spyOn(mcpApi, 'createMcp').mockResolvedValue(undefined);
		render(Page);

		await waitFor(() => expect(screen.getByRole('button', { name: 'Add MCP server' })).toBeInTheDocument());
		await fireEvent.click(screen.getByRole('button', { name: 'Add MCP server' }));

		await fireEvent.input(screen.getByPlaceholderText('name'), { target: { value: 'new-one' } });
		await fireEvent.input(screen.getByPlaceholderText('https://…'), {
			target: { value: 'https://new.test/mcp' }
		});
		await fireEvent.input(screen.getByPlaceholderText('short name used as the tool prefix'), {
			target: { value: 'browser' }
		});
		await fireEvent.input(screen.getByPlaceholderText('e.g. use for current info: weather, news, prices'), {
			target: { value: 'use for current weather' }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Add server' }));

		await waitFor(() =>
			expect(createSpy).toHaveBeenCalledWith(
				expect.objectContaining({ alias: 'browser', hint: 'use for current weather' })
			)
		);
	});

	it('shows the alias badge on the list when set', async () => {
		vi.spyOn(mcpApi, 'listMcp').mockResolvedValue({
			servers: [{ ...globalServer, alias: 'browser' }],
			canAdd: false
		});
		render(Page);

		await waitFor(() => expect(screen.getByText('shared-server')).toBeInTheDocument());
		expect(screen.getByText('browser')).toBeInTheDocument();
	});

	it('does not render the add form or button when canAdd is false', async () => {
		vi.spyOn(mcpApi, 'listMcp').mockResolvedValue({ servers: [ownServer], canAdd: false });
		render(Page);

		await waitFor(() => expect(screen.getByText('my-server')).toBeInTheDocument());
		expect(screen.queryByRole('button', { name: 'Add MCP server' })).toBeNull();
	});

	it('asks for confirmation before deleting, and cancel keeps the server', async () => {
		vi.spyOn(mcpApi, 'listMcp').mockResolvedValue({ servers: [ownServer], canAdd: true });
		const deleteSpy = vi.spyOn(mcpApi, 'deleteMcp').mockResolvedValue(undefined);
		render(Page);

		await waitFor(() => expect(screen.getByText('my-server')).toBeInTheDocument());
		await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
		const dialog = await screen.findByRole('dialog', { name: 'Delete MCP server' });
		expect(within(dialog).getByText(/my-server/)).toBeInTheDocument();

		await fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
		expect(deleteSpy).not.toHaveBeenCalled();
		expect(screen.getByText('my-server')).toBeInTheDocument();
	});

	it('deletes an editable server and reloads the list once confirmed', async () => {
		const listSpy = vi
			.spyOn(mcpApi, 'listMcp')
			.mockResolvedValueOnce({ servers: [ownServer], canAdd: true })
			.mockResolvedValueOnce({ servers: [], canAdd: true });
		const deleteSpy = vi.spyOn(mcpApi, 'deleteMcp').mockResolvedValue(undefined);
		render(Page);

		await waitFor(() => expect(screen.getByText('my-server')).toBeInTheDocument());
		await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
		const dialog = await screen.findByRole('dialog', { name: 'Delete MCP server' });
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));

		await waitFor(() => expect(deleteSpy).toHaveBeenCalledWith(7));
		await waitFor(() => expect(listSpy).toHaveBeenCalledTimes(2));
	});

	it('surfaces the API error message inline when create fails', async () => {
		vi.spyOn(mcpApi, 'listMcp').mockResolvedValue({ servers: [], canAdd: true });
		vi.spyOn(mcpApi, 'createMcp').mockRejectedValue(new Error('host not allowlisted'));
		render(Page);

		await waitFor(() => expect(screen.getByRole('button', { name: 'Add MCP server' })).toBeInTheDocument());
		await fireEvent.click(screen.getByRole('button', { name: 'Add MCP server' }));
		await fireEvent.input(screen.getByPlaceholderText('name'), { target: { value: 'bad' } });
		await fireEvent.input(screen.getByPlaceholderText('https://…'), {
			target: { value: 'https://blocked.test/mcp' }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Add server' }));

		await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('host not allowlisted'));
	});
});
