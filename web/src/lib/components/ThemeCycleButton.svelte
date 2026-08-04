<script lang="ts">
	import { THEME_LABEL } from '$lib/theme/constants';
	import { nextPreference } from '$lib/theme/resolve';
	import { setPreference, themePreference } from '$lib/theme/store';

	const upcoming = $derived(nextPreference($themePreference));
</script>

<button
	type="button"
	class="theme-cycle"
	aria-label={`Switch theme to ${THEME_LABEL[upcoming]}`}
	onclick={() => setPreference(upcoming)}
>
	<svg aria-hidden="true" viewBox="0 0 16 16">
		<circle cx="8" cy="8" r="5.25" />
		<path class="fill" d="M8 2.75a5.25 5.25 0 0 0 0 10.5z" />
	</svg>
</button>

<style>
	.theme-cycle {
		display: grid;
		flex: 0 0 auto;
		width: 30px;
		height: 30px;
		place-items: center;
		border: 0;
		border-radius: 5px;
		background: transparent;
		color: var(--text-muted);
		cursor: pointer;
		padding: 0;
	}
	.theme-cycle:hover,
	.theme-cycle:focus-visible {
		background: var(--bg);
		color: var(--text);
	}
	.theme-cycle svg {
		width: 16px;
		height: 16px;
		fill: none;
		stroke: currentColor;
		stroke-width: 1.5;
		stroke-linecap: round;
		stroke-linejoin: round;
	}
	.theme-cycle svg .fill {
		fill: currentColor;
		stroke: none;
	}
</style>
