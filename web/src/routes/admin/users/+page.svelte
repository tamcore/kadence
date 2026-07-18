<script lang="ts">
	import { onMount } from 'svelte';
	import { get } from 'svelte/store';
	import { goto } from '$app/navigation';
	import { api } from '$lib/api/client';
	import { isAdmin } from '$lib/stores/auth';
	import type { User } from '$lib/types';
	import Button from '$lib/components/Button.svelte';
	import Input from '$lib/components/Input.svelte';

	let users = $state<User[]>([]);
	let error = $state('');
	let loading = $state(true);

	let username = $state('');
	let email = $state('');
	let password = $state('');
	let role = $state<'user' | 'admin'>('user');

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

	async function handleCreate(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		try {
			await api.createUser({ username, email, password, role });
			username = email = password = '';
			role = 'user';
			await load();
		} catch {
			error = 'Could not create user (username or email may already exist)';
		}
	}

	async function handleDelete(id: number) {
		try {
			await api.deleteUser(id);
			await load();
		} catch {
			error = 'Could not delete user';
		}
	}

	onMount(() => {
		if (!get(isAdmin)) {
			goto('/');
			return;
		}
		load();
	});
</script>

<h1>Users</h1>
{#if error}<div class="error" role="alert">{error}</div>{/if}

<form class="create" onsubmit={handleCreate}>
	<Input label="Username" name="new-username" required bind:value={username} />
	<Input label="Email" name="new-email" type="email" required bind:value={email} />
	<Input label="Password" name="new-password" type="password" required bind:value={password} />
	<label class="role">
		<span>Role</span>
		<select bind:value={role}>
			<option value="user">user</option>
			<option value="admin">admin</option>
		</select>
	</label>
	<Button type="submit" variant="primary">Create user</Button>
</form>

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
					<td><Button variant="danger" onclick={() => handleDelete(u.id)}>Delete</Button></td>
				</tr>
			{/each}
		</tbody>
	</table>
{/if}

<style>
	.error { color: var(--danger); margin-bottom: 12px; }
	.muted { color: var(--text-muted); }
	.create { display: grid; gap: 8px; max-width: 360px; margin-bottom: 32px; }
	.role { display: flex; flex-direction: column; gap: 4px; margin-bottom: 12px; }
	.role span { font-size: 0.85rem; color: var(--text-muted); }
	select { padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--radius); font: inherit; background: var(--surface); }
	table { width: 100%; border-collapse: collapse; }
	th, td { text-align: left; padding: 10px; border-bottom: 1px solid var(--border); }
</style>
