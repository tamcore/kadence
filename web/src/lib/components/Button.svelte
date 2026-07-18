<script lang="ts">
	let {
		variant = 'primary',
		type = 'button',
		loading = false,
		disabled = false,
		children,
		onclick
	}: {
		variant?: 'primary' | 'danger' | 'ghost';
		type?: 'button' | 'submit';
		loading?: boolean;
		disabled?: boolean;
		children: import('svelte').Snippet;
		onclick?: () => void;
	} = $props();
</script>

<button {type} class="btn {variant}" disabled={disabled || loading} {onclick}>
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
	.primary { background: var(--accent); color: #fff; }
	.primary:hover:not(:disabled) { background: var(--accent-hover); }
	.danger { background: var(--danger); color: #fff; }
	.ghost { background: transparent; border-color: var(--border); color: var(--text); }
</style>
