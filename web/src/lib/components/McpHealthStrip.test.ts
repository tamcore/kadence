import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import McpHealthStrip from './McpHealthStrip.svelte';

describe('McpHealthStrip', () => {
	it('shows count when unhealthy > 0', () => {
		render(McpHealthStrip, { unhealthy: 2, total: 5 });
		expect(screen.getByRole('status').textContent).toContain('2 of 5');
	});

	it('renders nothing when unhealthy is 0', () => {
		render(McpHealthStrip, { unhealthy: 0, total: 5 });
		expect(screen.queryByRole('status')).toBeNull();
	});
});
