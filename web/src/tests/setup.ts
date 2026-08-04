import '@testing-library/jest-dom/vitest';

// Polyfill localStorage for jsdom tests
if (typeof localStorage === 'undefined') {
	const store: Record<string, string> = {};

	Object.defineProperty(globalThis, 'localStorage', {
		value: {
			getItem: (key: string) => store[key] ?? null,
			setItem: (key: string, value: string) => {
				store[key] = value.toString();
			},
			removeItem: (key: string) => {
				delete store[key];
			},
			clear: () => {
				for (const key in store) {
					delete store[key];
				}
			},
			key: (index: number) => {
				const keys = Object.keys(store);
				return keys[index] ?? null;
			},
			length: Object.keys(store).length
		},
		writable: false
	});
}

// jsdom does not implement matchMedia. Theme resolution reads
// prefers-color-scheme, so every test that renders the layout, the sidebar or
// the profile page needs this. Defaults to light.
// `configurable: true` is REQUIRED: defineProperty defaults it to false, and
// store.test.ts re-defines matchMedia per case. Without it those tests throw
// "TypeError: Cannot redefine property: matchMedia".
if (typeof window !== 'undefined' && typeof window.matchMedia !== 'function') {
	Object.defineProperty(window, 'matchMedia', {
		configurable: true,
		writable: true,
		value: (query: string) => ({
			matches: false,
			media: query,
			onchange: null,
			addEventListener: () => {},
			removeEventListener: () => {},
			addListener: () => {},
			removeListener: () => {},
			dispatchEvent: () => false
		})
	});
}
