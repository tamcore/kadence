<script lang="ts">
	import { currentUser, setAuth } from '$lib/stores/auth';
	import { updateProfile, changePassword } from '$lib/api/profile';
	import Button from '$lib/components/Button.svelte';
	import Input from '$lib/components/Input.svelte';

	let form = $state({
		displayName: $currentUser?.displayName ?? '',
		email: $currentUser?.email ?? '',
		unitSystem: ($currentUser?.unitSystem ?? 'metric') as 'metric' | 'imperial'
	});

	let pw = $state({ currentPassword: '', newPassword: '', logoutOthers: true });

	let msg = $state<string | null>(null);
	let err = $state<string | null>(null);

	async function saveProfile(e: SubmitEvent): Promise<void> {
		e.preventDefault();
		err = null;
		msg = null;
		try {
			setAuth(await updateProfile(form));
			msg = 'Saved';
		} catch (e) {
			err = e instanceof Error ? e.message : 'Save failed';
		}
	}

	async function savePassword(e: SubmitEvent): Promise<void> {
		e.preventDefault();
		err = null;
		msg = null;
		try {
			await changePassword(pw);
			pw = { currentPassword: '', newPassword: '', logoutOthers: true };
			msg = 'Password changed';
		} catch (e) {
			err = e instanceof Error ? e.message : 'Change failed';
		}
	}
</script>

<div class="page">
	<h1>Profile</h1>

	{#if err}<div class="error" role="alert">{err}</div>{/if}
	{#if msg}<div class="ok">{msg}</div>{/if}

	<section>
		<h2>Account</h2>
		<form class="form" onsubmit={saveProfile}>
			<Input label="Display name" name="displayName" required bind:value={form.displayName} />
			<Input label="Email" name="email" type="email" required bind:value={form.email} />
			<Button type="submit" variant="primary">Save account</Button>
		</form>
	</section>

	<section>
		<h2>Preferences</h2>
		<form class="form" onsubmit={saveProfile}>
			<fieldset class="units">
				<legend>Unit system</legend>
				<label>
					<input type="radio" name="unitSystem" value="metric" bind:group={form.unitSystem} />
					Metric
				</label>
				<label>
					<input type="radio" name="unitSystem" value="imperial" bind:group={form.unitSystem} />
					Imperial
				</label>
			</fieldset>
			<Button type="submit" variant="primary">Save preferences</Button>
		</form>
	</section>

	<section>
		<h2>Password</h2>
		<form class="form" onsubmit={savePassword}>
			<Input
				label="Current password"
				name="currentPassword"
				type="password"
				required
				bind:value={pw.currentPassword}
			/>
			<Input
				label="New password"
				name="newPassword"
				type="password"
				required
				bind:value={pw.newPassword}
			/>
			<label class="checkbox">
				<input type="checkbox" bind:checked={pw.logoutOthers} />
				Log out other devices
			</label>
			<Button type="submit" variant="primary">Change password</Button>
		</form>
	</section>
</div>

<style>
	.error {
		color: var(--danger);
		margin-bottom: 12px;
	}
	.ok {
		color: var(--accent);
		margin-bottom: 12px;
	}
	section {
		max-width: 360px;
		margin-bottom: 32px;
	}
	section h2 {
		font-size: 1rem;
		margin-bottom: 12px;
	}
	.form {
		display: grid;
		gap: 8px;
	}
	.units {
		display: flex;
		flex-direction: column;
		gap: 4px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 10px 12px;
		margin-bottom: 12px;
	}
	.units legend {
		font-size: 0.85rem;
		color: var(--text-muted);
		padding: 0 4px;
	}
	.checkbox {
		display: flex;
		align-items: center;
		gap: 8px;
		margin-bottom: 12px;
	}
</style>
