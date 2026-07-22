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
		} catch {
			error = 'Could not load users';
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
		} catch {
			error = 'Could not delete user';
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
		<table>
			<thead><tr><th>Username</th><th>Email</th><th>Role</th><th></th></tr></thead>
			<tbody>
				{#each users as u (u.id)}
					<tr>
						<td>{u.username}</td>
						<td>{u.email}</td>
						<td>{u.role}</td>
						<td class="row-actions">
							<Button variant="ghost" onclick={() => openEdit(u)}>Edit</Button>
							<Button variant="danger" onclick={() => requestDelete(u)}>Delete</Button>
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
		display: flex;
		gap: 8px;
		justify-content: flex-end;
	}
</style>
