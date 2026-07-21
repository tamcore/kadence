<script lang="ts">
	import { onMount } from 'svelte';
	import { currentUser, setAuth } from '$lib/stores/auth';
	import { updateProfile, changePassword } from '$lib/api/profile';
	import { listSessions, revokeSession, revokeOtherSessions, type Session } from '$lib/api/sessions';
	import {
		isWebAuthnEnabled,
		registerPasskey,
		listPasskeys,
		renamePasskey,
		deletePasskey,
		type Passkey
	} from '$lib/api/webauthn';
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

	let sessions = $state<Session[]>([]);

	async function loadSessions(): Promise<void> {
		try {
			sessions = await listSessions();
		} catch (e) {
			err = e instanceof Error ? e.message : 'Could not load sessions';
		}
	}

	async function revoke(publicId: string): Promise<void> {
		try {
			await revokeSession(publicId);
			await loadSessions();
		} catch (e) {
			err = e instanceof Error ? e.message : 'Revoke failed';
		}
	}

	async function signOutOthers(): Promise<void> {
		try {
			await revokeOtherSessions();
			await loadSessions();
		} catch (e) {
			err = e instanceof Error ? e.message : 'Sign-out failed';
		}
	}

	const SECONDS_PER_MINUTE = 60;
	const SECONDS_PER_HOUR = 3600;
	const SECONDS_PER_DAY = 86400;

	function ago(iso: string): string {
		const seconds = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
		if (seconds < SECONDS_PER_MINUTE) return 'just now';
		if (seconds < SECONDS_PER_HOUR) return `${Math.floor(seconds / SECONDS_PER_MINUTE)}m ago`;
		if (seconds < SECONDS_PER_DAY) return `${Math.floor(seconds / SECONDS_PER_HOUR)}h ago`;
		return `${Math.floor(seconds / SECONDS_PER_DAY)}d ago`;
	}

	let passkeysEnabled = $state(false);
	let passkeys = $state<Passkey[]>([]);

	async function loadPasskeys(): Promise<void> {
		passkeysEnabled = await isWebAuthnEnabled();
		if (!passkeysEnabled) return;
		try {
			passkeys = await listPasskeys();
		} catch (e) {
			err = e instanceof Error ? e.message : 'Could not load passkeys';
		}
	}

	async function addPasskey(): Promise<void> {
		const name = (prompt('Name this passkey', 'My device') ?? '').trim();
		if (!name) return;
		err = null;
		msg = null;
		try {
			await registerPasskey(name);
			msg = 'Passkey added';
			await loadPasskeys();
		} catch (e) {
			err =
				e instanceof Error && e.name === 'NotAllowedError'
					? 'Passkey registration cancelled'
					: 'Could not add passkey';
		}
	}

	async function renamePk(publicId: string): Promise<void> {
		const name = (prompt('New name') ?? '').trim();
		if (!name) return;
		try {
			await renamePasskey(publicId, name);
			await loadPasskeys();
		} catch (e) {
			err = e instanceof Error ? e.message : 'Rename failed';
		}
	}

	async function deletePk(publicId: string): Promise<void> {
		try {
			await deletePasskey(publicId);
			await loadPasskeys();
		} catch (e) {
			err = e instanceof Error ? e.message : 'Delete failed';
		}
	}

	onMount(() => {
		void loadSessions();
		void loadPasskeys();
	});
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

	<section class="sessions-section">
		<h2>Active sessions</h2>
		<Button variant="ghost" onclick={signOutOthers}>Sign out other devices</Button>
		<ul class="sessions">
			{#each sessions as s (s.publicId)}
				<li>
					<div class="session-info">
						<span class="dev">{s.device}</span>
						<span class="muted"
							>{s.ip} · last active {ago(s.lastSeenAt)} · signed in {new Date(
								s.createdAt
							).toLocaleDateString()}</span
						>
					</div>
					{#if s.current}
						<span class="badge">This device</span>
					{:else}
						<Button variant="ghost" onclick={() => revoke(s.publicId)}>Revoke</Button>
					{/if}
				</li>
			{/each}
		</ul>
	</section>

	{#if passkeysEnabled}
		<section class="passkeys-section">
			<h2>Passkeys</h2>
			<Button variant="ghost" onclick={addPasskey}>Add a passkey</Button>
			<ul class="passkeys">
				{#each passkeys as p (p.publicId)}
					<li>
						<div class="passkey-info">
							<span class="dev">{p.name}</span>
							<span class="muted"
								>added {new Date(p.createdAt).toLocaleDateString()}{p.lastUsedAt
									? ` · last used ${new Date(p.lastUsedAt).toLocaleDateString()}`
									: ''}</span
							>
						</div>
						<div class="passkey-actions">
							<Button variant="ghost" onclick={() => renamePk(p.publicId)}>Rename</Button>
							<Button variant="ghost" onclick={() => deletePk(p.publicId)}>Delete</Button>
						</div>
					</li>
				{/each}
				{#if passkeys.length === 0}
					<li class="muted">No passkeys yet.</li>
				{/if}
			</ul>
		</section>
	{/if}
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
	.sessions-section {
		max-width: 560px;
	}
	.sessions {
		list-style: none;
		margin-top: 12px;
		padding: 0;
		display: grid;
		gap: 8px;
	}
	.sessions li {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 10px 12px;
	}
	.session-info {
		display: flex;
		flex-direction: column;
		gap: 2px;
		min-width: 0;
	}
	.dev {
		font-weight: 600;
	}
	.muted {
		font-size: 0.85rem;
		color: var(--text-muted);
	}
	.badge {
		font-size: 0.8rem;
		font-weight: 600;
		color: var(--accent);
		border: 1px solid var(--accent);
		border-radius: var(--radius);
		padding: 4px 8px;
		white-space: nowrap;
	}
	.passkeys-section {
		max-width: 560px;
	}
	.passkeys {
		list-style: none;
		margin-top: 12px;
		padding: 0;
		display: grid;
		gap: 8px;
	}
	.passkeys li {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 10px 12px;
	}
	.passkey-info {
		display: flex;
		flex-direction: column;
		gap: 2px;
		min-width: 0;
	}
	.passkey-actions {
		display: flex;
		gap: 8px;
	}
</style>
