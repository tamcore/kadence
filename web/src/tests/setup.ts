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
