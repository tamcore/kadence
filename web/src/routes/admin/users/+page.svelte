<script lang="ts">
	import { onMount } from 'svelte';
	import { get } from 'svelte/store';
	import { goto } from '$app/navigation';
	import { api } from '$lib/api/client';
	import { isAdmin } from '$lib/stores/auth';
	import type { User } from '$lib/types';
	import Button from '$lib/components/Button.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import UserForm from '$lib/components/UserForm.svelte';
	import ActionMenu from '$lib/components/ActionMenu.svelte';

	let users = $state<User[]>([]);
	let error = $state('');
	let loading = $state(true);

	let modalMode = $state<'create' | 'edit' | null>(null);
	let editing = $state<User | undefined>(undefined);
	let deleteTarget = $state<User | null>(null);

	async function load() {
		loading = true;
		error = '';
		try {
			users = await api.listUsers();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Could not load users';
		} finally {
			loading = false;
		}
	}

	function openCreate() {
		editing = undefined;
		modalMode = 'create';
	}

	function openEdit(u: User) {
		editing = u;
		modalMode = 'edit';
	}

	function closeModal() {
		modalMode = null;
		editing = undefined;
	}

	async function onSaved() {
		closeModal();
		await load();
	}

	async function handleDelete(id: number) {
		try {
			await api.deleteUser(id);
			await load();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Could not delete user';
		}
	}

	function requestDelete(u: User): void {
		deleteTarget = u;
	}

	async function confirmDelete(): Promise<void> {
		const u = deleteTarget;
		deleteTarget = null;
		if (u) await handleDelete(u.id);
	}

	onMount(() => {
		if (!get(isAdmin)) {
			goto('/');
			return;
		}
		load();
	});
</script>

<div class="page">
	<div class="header">
		<h1>Users</h1>
		<Button variant="primary" onclick={openCreate}>New user</Button>
	</div>

	{#if error}<div class="error" role="alert">{error}</div>{/if}

	{#if loading}
		<p class="muted">Loading…</p>
	{:else}
		<table aria-label="Users">
			<thead><tr><th>Username</th><th>Email</th><th>Role</th><th></th></tr></thead>
			<tbody>
				{#each users as u (u.id)}
					<tr>
						<td><span class="mobile-label">Username</span><strong>{u.username}</strong></td>
						<td><span class="mobile-label">Email</span><span>{u.email}</span></td>
						<td><span class="mobile-label">Role</span><span>{u.role}</span></td>
						<td class="row-actions">
							<span class="desktop-actions">
								<Button variant="ghost" onclick={() => openEdit(u)}>Edit</Button>
								<Button variant="danger" onclick={() => requestDelete(u)}>Delete</Button>
							</span>
							<span class="mobile-actions">
								<ActionMenu
									label={`Actions for ${u.username}`}
									items={[
										{ label: 'Edit', onSelect: () => openEdit(u) },
										{ label: 'Delete', danger: true, onSelect: () => requestDelete(u) }
									]}
								/>
							</span>
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	{/if}
</div>

<Modal
	open={modalMode !== null}
	title={modalMode === 'edit' ? 'Edit user' : 'New user'}
	onClose={closeModal}
>
	{#if modalMode}
		{#key editing?.id ?? 'create'}
			<UserForm mode={modalMode} user={editing} onSuccess={onSaved} onCancel={closeModal} />
		{/key}
	{/if}
</Modal>

<ConfirmDialog
	open={deleteTarget !== null}
	title="Delete user"
	message={`Delete ${deleteTarget?.username}? This cannot be undone.`}
	onConfirm={confirmDelete}
	onCancel={() => (deleteTarget = null)}
/>

<style>
	.header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 16px;
	}
	.header h1 {
		margin: 0;
	}
	.error {
		color: var(--danger);
		margin-bottom: 12px;
	}
	.muted {
		color: var(--text-muted);
	}
	table {
		width: 100%;
		border-collapse: collapse;
	}
	th,
	td {
		text-align: left;
		padding: 10px;
		border-bottom: 1px solid var(--border);
	}
	.row-actions {
		text-align: right;
	}
	.desktop-actions { display: inline-flex; gap: 8px; }
	.mobile-label, .mobile-actions { display: none; }

	@media (max-width: 700px) {
		table, tbody { display: block; width: 100%; min-width: 0; }
		thead {
			position: absolute;
			width: 1px;
			height: 1px;
			padding: 0;
			margin: -1px;
			overflow: hidden;
			clip: rect(0, 0, 0, 0);
			white-space: nowrap;
			border: 0;
		}
		tbody { display: grid; gap: 10px; }
		tr {
			position: relative;
			display: block;
			min-width: 0;
			padding: 12px 48px 12px 14px;
			border: 1px solid var(--border);
			border-radius: calc(var(--radius) + 2px);
			background: var(--surface);
		}
		td {
			display: grid;
			grid-template-columns: 72px minmax(0, 1fr);
			gap: 10px;
			min-width: 0;
			padding: 4px 0;
			border: 0;
			overflow-wrap: anywhere;
		}
		.mobile-label {
			display: block;
			color: var(--text-muted);
			font-size: 0.72rem;
			font-weight: 650;
			letter-spacing: 0.04em;
			text-transform: uppercase;
		}
		.row-actions {
			position: absolute;
			top: 7px;
			right: 7px;
			display: block;
			width: auto;
			padding: 0;
		}
		.desktop-actions { display: none; }
		.mobile-actions { display: block; }
	}
</style>
