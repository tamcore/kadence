import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import ReindexStrip from './ReindexStrip.svelte';

describe('ReindexStrip', () => {
	it('shows progress when stale > 0', () => {
		render(ReindexStrip, { stale: 3, total: 10 });
		expect(screen.getByRole('status').textContent).toContain('7 of 10');
	});

	it('renders nothing when stale is 0', () => {
		render(ReindexStrip, { stale: 0, total: 10 });
		expect(screen.queryByRole('status')).toBeNull();
	});
});
