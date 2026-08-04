<script lang="ts">
	import {
		DARK_VARIANTS,
		DARK_VARIANT_LABEL,
		THEME_LABEL,
		THEME_PREFERENCES
	} from '$lib/theme/constants';
	import { darkVariant, setDarkVariant, setPreference, themePreference } from '$lib/theme/store';
</script>

<fieldset class="group">
	<legend>Appearance</legend>
	{#each THEME_PREFERENCES as pref (pref)}
		<div class="row">
			<input
				type="radio"
				id={`theme-${pref}`}
				name="themePreference"
				value={pref}
				checked={$themePreference === pref}
				onchange={() => setPreference(pref)}
			/>
			<label for={`theme-${pref}`}>{THEME_LABEL[pref]}</label>
		</div>
		{#if pref === 'auto' && $themePreference === 'auto'}
			<fieldset class="group nested">
				<legend>When dark, use</legend>
				{#each DARK_VARIANTS as variant (variant)}
					<div class="row">
						<input
							type="radio"
							id={`theme-variant-${variant}`}
							name="themeDarkVariant"
							value={variant}
							checked={$darkVariant === variant}
							onchange={() => setDarkVariant(variant)}
						/>
						<label for={`theme-variant-${variant}`}>{DARK_VARIANT_LABEL[variant]}</label>
					</div>
				{/each}
			</fieldset>
		{/if}
	{/each}
</fieldset>
<p class="hint">Auto follows your device. Saved on this device only, not to your account.</p>

<style>
	.group {
		display: flex;
		flex-direction: column;
		gap: 4px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 10px 12px;
		margin-bottom: 12px;
	}
	.group legend {
		font-size: 0.85rem;
		color: var(--text-muted);
		padding: 0 4px;
	}
	.nested {
		margin: 4px 0 4px 22px;
	}
	.row {
		display: flex;
		align-items: center;
		gap: 6px;
	}
	.hint {
		margin: 0;
		font-size: 0.85rem;
		color: var(--text-muted);
	}
</style>
