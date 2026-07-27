<script lang="ts">
	let {
		variant = 'primary',
		type = 'button',
		loading = false,
		disabled = false,
		fullWidth = false,
		children,
		onclick,
		...rest
	}: {
		variant?: 'primary' | 'danger' | 'ghost';
		type?: 'button' | 'submit';
		loading?: boolean;
		disabled?: boolean;
		fullWidth?: boolean;
		children: import('svelte').Snippet;
		onclick?: () => void;
		[key: string]: unknown;
	} = $props();
</script>

<button
	{type}
	class="btn {variant}"
	class:full-width={fullWidth}
	disabled={disabled || loading}
	{onclick}
	{...rest}
>
	{@render children()}
</button>

<style>
	.btn {
		padding: 10px 16px;
		border: 1px solid transparent;
		border-radius: var(--radius);
		font: inherit;
		font-weight: 600;
		cursor: pointer;
		transition: background 0.15s ease;
	}
	.btn:disabled { opacity: 0.6; cursor: not-allowed; }
	.full-width { width: 100%; }
	.primary { background: var(--accent); color: #fff; }
	.primary:hover:not(:disabled) { background: var(--accent-hover); }
	.danger { background: var(--danger); color: #fff; }
	.ghost { background: transparent; border-color: var(--border); color: var(--text); }
</style>
