import { writable } from 'svelte/store';

// Sidebar visibility (drawer on mobile, persistent on desktop). Default open;
// the layout closes it on mobile after navigation.
export const sidebarOpen = writable(true);
export function toggleSidebar(): void {
	sidebarOpen.update((v) => !v);
}
export function closeSidebar(): void {
	sidebarOpen.set(false);
}
