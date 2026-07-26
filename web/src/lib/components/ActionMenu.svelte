<script lang="ts">
	import { onMount, tick, type Snippet } from 'svelte';

export type ActionMenuItem =
	| { separator: true; label?: never; href?: never; danger?: never; disabled?: never; onSelect?: never }
	| {
				separator?: false;
				label: string;
				ariaLabel?: string;
				href?: string;
				danger?: boolean;
				disabled?: boolean;
				onSelect?: () => void | Promise<void>;
		  };

	export interface ActionMenuTrigger {
		open: () => void;
		close: () => void;
		expanded: boolean;
		menuId: string;
	}

	interface Props {
		label: string;
		items: ActionMenuItem[];
		trigger?: Snippet<[ActionMenuTrigger]>;
	}

	let { label, items, trigger }: Props = $props();
	let open = $state(false);
	let triggerElement = $state<HTMLElement>();
	const menuId = $props.id();
	const openedEvent = 'kadence:action-menu-opened';
	function getMenu(): HTMLDivElement | undefined {
		const element = document.getElementById(menuId);
		return element instanceof HTMLDivElement ? element : undefined;
	}

	onMount(() => {
		const closeOtherMenu = (event: Event) => {
			if ((event as CustomEvent<string>).detail !== menuId) closeMenu(false);
		};
		window.addEventListener(openedEvent, closeOtherMenu);
		return () => window.removeEventListener(openedEvent, closeOtherMenu);
	});

	function findTrigger(target: EventTarget | null): HTMLElement | undefined {
		return target instanceof HTMLElement ? target.closest('[data-action-menu-trigger]') ?? undefined : undefined;
	}

	function placeMenu(): void {
		const menu = getMenu();
		if (!menu || !triggerElement) return;
		const rect = triggerElement.getBoundingClientRect();
		const edge = 8;
		const gap = 6;
		const menuRect = menu.getBoundingClientRect();
		const width = Math.min(menuRect.width || menu.offsetWidth || 224, window.innerWidth - edge * 2);
		const height = Math.min(menuRect.height || menu.offsetHeight || 240, window.innerHeight - edge * 2);
		const left = Math.max(edge, Math.min(rect.right - width, window.innerWidth - width - edge));
		const below = rect.bottom + gap;
		const above = rect.top - height - gap;
		const top = Math.max(edge, Math.min(below + height > window.innerHeight - edge ? above : below, window.innerHeight - height - edge));
		const maxHeight = Math.max(0, window.innerHeight - top - edge);
		menu.style.setProperty('--action-menu-left', `${left}px`);
		menu.style.setProperty('--action-menu-top', `${top}px`);
		menu.style.setProperty('max-height', `${maxHeight}px`);
		menu.style.setProperty('overflow-y', 'auto');
	}

	function openMenu(event?: Event): void {
		triggerElement = findTrigger(event?.currentTarget ?? null) ?? triggerElement;
		window.dispatchEvent(new CustomEvent(openedEvent, { detail: menuId }));
		const currentlyOpen = document.querySelector<HTMLElement>('[data-action-menu-open]');
		if (currentlyOpen && currentlyOpen !== getMenu()) currentlyOpen.hidePopover?.();
		open = true;
		tick().then(() => {
			getMenu()?.showPopover?.();
			placeMenu();
			focusItem(1, 'first');
		});
	}

	function closeMenu(restoreFocus = true): void {
		if (!open) return;
		getMenu()?.hidePopover?.();
		open = false;
		if (restoreFocus) tick().then(() => triggerElement?.focus());
	}

	function onToggle(event: ToggleEvent): void {
		if (event.newState === 'closed' && open) closeMenu(true);
	}

	function onTriggerClick(event: MouseEvent): void {
		triggerElement = findTrigger(event.currentTarget);
		if (open) closeMenu(false);
		else openMenu(event);
	}

	function onDocumentPointerDown(event: PointerEvent): void {
		const menu = getMenu();
		if (!open || !menu || !triggerElement) return;
		const target = event.target as Node | null;
		if (target && !menu.contains(target) && !triggerElement.contains(target)) closeMenu();
	}

	function focusItem(direction: 1 | -1, start?: 'first' | 'last'): void {
		const candidates = Array.from(getMenu()?.querySelectorAll<HTMLElement>('[role="menuitem"]:not([disabled])') ?? []);
		if (!candidates.length) return;
		if (start === 'first') return candidates[0].focus();
		if (start === 'last') return candidates.at(-1)?.focus();
		const current = candidates.indexOf(document.activeElement as HTMLElement);
		candidates[(current + direction + candidates.length) % candidates.length].focus();
	}

	function onMenuKeydown(event: KeyboardEvent): void {
		switch (event.key) {
			case 'Escape':
				event.preventDefault();
				closeMenu();
				break;
			case 'ArrowDown':
				event.preventDefault();
				focusItem(1);
				break;
			case 'ArrowUp':
				event.preventDefault();
				focusItem(-1);
				break;
			case 'Home':
				event.preventDefault();
				focusItem(1, 'first');
				break;
			case 'End':
				event.preventDefault();
				focusItem(-1, 'last');
		}
	}

	async function select(item: Exclude<ActionMenuItem, { separator: true }>): Promise<void> {
		if (item.disabled) return;
		await item.onSelect?.();
		closeMenu();
	}
</script>

<svelte:document onpointerdown={onDocumentPointerDown} />

{#if trigger}
	{@render trigger({ open: openMenu, close: closeMenu, expanded: open, menuId })}
{:else}
	<button
		type="button"
		class="action-menu-trigger"
		data-action-menu-trigger
		aria-label={label}
		aria-haspopup="menu"
		aria-expanded={open}
		aria-controls={open ? menuId : undefined}
		onclick={onTriggerClick}
	>
		<span aria-hidden="true">•••</span>
	</button>
{/if}

{#if open}
	<div
		id={menuId}
		class="action-menu"
		data-action-menu-open
		popover="auto"
		role="menu"
		aria-label={label}
		tabindex="-1"
		onkeydown={onMenuKeydown}
		ontoggle={onToggle}
	>
		{#each items as item, index (`${index}-${item.separator ? 'separator' : item.label}`)}
			{#if item.separator}
				<div class="action-menu-separator" role="separator"></div>
			{:else if item.href && !item.disabled}
				<a class:danger={item.danger} role="menuitem" href={item.href} aria-label={item.ariaLabel} onclick={() => void select(item)}>{item.label}</a>
			{:else}
				<button
					type="button"
					class:danger={item.danger}
					role="menuitem"
					disabled={item.disabled}
					aria-disabled={item.disabled ? 'true' : undefined}
					aria-label={item.ariaLabel}
					onclick={() => void select(item)}
				>{item.label}</button>
			{/if}
		{/each}
	</div>
{/if}

<style>
	.action-menu-trigger {
		border: 0;
		border-radius: 6px;
		background: transparent;
		color: var(--text-muted);
		cursor: pointer;
		font: inherit;
		line-height: 1;
		padding: 6px 8px;
	}
	.action-menu-trigger:hover, .action-menu-trigger:focus-visible { background: var(--bg); color: var(--text); }
	.action-menu {
		position: fixed;
		inset: var(--action-menu-top) auto auto var(--action-menu-left);
		width: min(224px, calc(100vw - 16px));
		max-height: calc(100vh - 16px);
		overflow-y: auto;
		margin: 0;
		padding: 4px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--surface);
		box-shadow: var(--shadow);
	}
	.action-menu :is(button, a) {
		display: block;
		width: 100%;
		border: 0;
		border-radius: 5px;
		background: transparent;
		color: var(--text);
		cursor: pointer;
		font: inherit;
		padding: 8px 10px;
		text-align: left;
		text-decoration: none;
	}
	.action-menu :is(button, a):hover, .action-menu :is(button, a):focus-visible { background: var(--bg); }
	.action-menu :is(button, a).danger { color: var(--danger); }
	.action-menu button:disabled { color: var(--text-muted); cursor: not-allowed; opacity: 0.6; }
	.action-menu-separator { height: 1px; margin: 4px; background: var(--border); }
</style>
