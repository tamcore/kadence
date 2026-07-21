<script lang="ts">
	import { api } from '$lib/api/client';
	import type { User } from '$lib/types';
	import Button from '$lib/components/Button.svelte';
	import Input from '$lib/components/Input.svelte';

	let {
		mode,
		user,
		onSuccess,
		onCancel
	}: {
		mode: 'create' | 'edit';
		user?: User;
		onSuccess: () => void;
		onCancel: () => void;
	} = $props();

	let username = $state(user?.username ?? '');
	let email = $state(user?.email ?? '');
	let password = $state('');
	let role = $state<'user' | 'admin'>(user?.role ?? 'user');
	let error = $state('');
	let saving = $state(false);

	async function handleSubmit(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		saving = true;
		try {
			if (mode === 'create') {
				await api.createUser({ username, email, password, role });
			} else if (user) {
				await api.updateUser(user.id, {
					username,
					email,
					role,
					...(password ? { password } : {})
				});
			}
			onSuccess();
		} catch {
			error =
				mode === 'create'
					? 'Could not create user (username or email may already exist)'
					: 'Could not update user (username or email may already exist)';
		} finally {
			saving = false;
		}
	}
</script>

<form class="user-form" onsubmit={handleSubmit}>
	{#if error}<div class="error" role="alert">{error}</div>{/if}

	<Input label="Username" name="uf-username" required bind:value={username} />
	<Input label="Email" name="uf-email" type="email" required bind:value={email} />
	<Input
		label={mode === 'create' ? 'Password' : 'New password (leave blank to keep)'}
		name="uf-password"
		type="password"
		required={mode === 'create'}
		bind:value={password}
	/>
	<label class="role">
		<span>Role</span>
		<select bind:value={role}>
			<option value="user">user</option>
			<option value="admin">admin</option>
		</select>
	</label>

	<div class="actions">
		<Button type="button" variant="ghost" onclick={onCancel}>Cancel</Button>
		<Button type="submit" variant="primary" loading={saving}>
			{mode === 'create' ? 'Create user' : 'Save changes'}
		</Button>
	</div>
</form>

<style>
	.user-form {
		display: grid;
		gap: 8px;
	}
	.error {
		color: var(--danger);
		margin-bottom: 8px;
	}
	.role {
		display: flex;
		flex-direction: column;
		gap: 4px;
		margin-bottom: 12px;
	}
	.role span {
		font-size: 0.85rem;
		color: var(--text-muted);
	}
	select {
		padding: 10px 12px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		font: inherit;
		background: var(--surface);
	}
	.actions {
		display: flex;
		justify-content: flex-end;
		gap: 8px;
		margin-top: 4px;
	}
</style>
