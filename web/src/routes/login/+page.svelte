<script lang="ts">
	import { goto } from '$app/navigation';
	import { api } from '$lib/api/client';
	import { setAuth } from '$lib/stores/auth';
	import { sanitizeReturnTo } from '$lib/util/returnTo';
	import { isWebAuthnEnabled, loginWithPasskey } from '$lib/api/webauthn';
	import Button from '$lib/components/Button.svelte';
	import Input from '$lib/components/Input.svelte';

	let username = $state('');
	let password = $state('');
	let remember = $state(false);
	let error = $state('');
	let loading = $state(false);

	let passkeysEnabled = $state(false);
	$effect(() => {
		void isWebAuthnEnabled().then((v) => (passkeysEnabled = v));
	});

	async function handleSubmit(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		loading = true;
		try {
			const user = await api.login(username, password, remember);
			setAuth(user);
			// Grab a CSRF token (GET is CSRF-protected and returns X-CSRF-Token) before any unsafe action.
			await api.getCurrentUser().catch(() => {});
			const returnTo = sanitizeReturnTo(new URLSearchParams(window.location.search).get('returnTo'));
			await goto(returnTo);
		} catch {
			error = 'Invalid username or password';
		} finally {
			loading = false;
		}
	}

	async function handlePasskey() {
		error = '';
		loading = true;
		try {
			const user = await loginWithPasskey();
			setAuth(user);
			await api.getCurrentUser().catch(() => {});
			const returnTo = sanitizeReturnTo(new URLSearchParams(window.location.search).get('returnTo'));
			await goto(returnTo);
		} catch (e) {
			error =
				e instanceof Error && e.name === 'NotAllowedError'
					? 'Passkey sign-in cancelled'
					: 'Passkey sign-in failed';
		} finally {
			loading = false;
		}
	}
</script>

<main class="login">
	<form class="card" onsubmit={handleSubmit}>
		<h1>Kadence</h1>
		<Input label="Username" name="username" required autocomplete="username" bind:value={username} />
		<Input label="Password" name="password" type="password" required autocomplete="current-password" bind:value={password} />
		<label class="remember">
			<input type="checkbox" bind:checked={remember} />
			<span>Remember me</span>
		</label>
		{#if error}<div class="error" role="alert">{error}</div>{/if}
		<Button type="submit" variant="primary" {loading}>{loading ? 'Logging in…' : 'Log in'}</Button>
		{#if passkeysEnabled}
			<div class="divider">or</div>
			<Button type="button" variant="ghost" onclick={handlePasskey} {loading}>🔑 Sign in with a passkey</Button>
		{/if}
	</form>
</main>

<style>
	.login { min-height: 100vh; display: grid; place-items: center; padding: 16px; }
	.card {
		background: var(--surface); padding: 32px; border-radius: var(--radius);
		box-shadow: var(--shadow); width: 100%; max-width: 360px;
	}
	h1 { margin: 0 0 24px; text-align: center; }
	.remember { display: flex; align-items: center; gap: 8px; margin: 8px 0 16px; font-size: 0.9rem; }
	.error { color: var(--danger); font-size: 0.9rem; margin-bottom: 12px; }
	.divider {
		display: flex; align-items: center; gap: 8px; margin: 16px 0;
		color: var(--text-muted); font-size: 0.85rem; text-align: center;
	}
	.divider::before, .divider::after {
		content: ''; flex: 1; height: 1px; background: var(--border);
	}
</style>
