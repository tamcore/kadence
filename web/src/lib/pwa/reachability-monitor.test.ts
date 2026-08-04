import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { ReachabilityMonitor, HEALTHY_INTERVAL_MS, UNHEALTHY_INTERVAL_MS } from './reachability-monitor';

function okResponse(ok: boolean) {
	return { ok } as Response;
}

describe('ReachabilityMonitor', () => {
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => vi.useRealTimers());

	it('marks reachable on a healthy probe', async () => {
		const fetchFn = vi.fn().mockResolvedValue(okResponse(true));
		const setReachable = vi.fn();
		const m = new ReachabilityMonitor(fetchFn, setReachable, () => true);
		await m.probeNow();
		expect(fetchFn).toHaveBeenCalledWith('/api/healthz', expect.objectContaining({ method: 'GET' }));
		expect(setReachable).toHaveBeenLastCalledWith(true);
	});

	it('marks unreachable when the probe throws', async () => {
		const fetchFn = vi.fn().mockRejectedValue(new TypeError('failed to fetch'));
		const setReachable = vi.fn();
		const m = new ReachabilityMonitor(fetchFn, setReachable, () => true);
		await m.probeNow();
		expect(setReachable).toHaveBeenLastCalledWith(false);
	});

	it('marks unreachable on a non-ok probe', async () => {
		const fetchFn = vi.fn().mockResolvedValue(okResponse(false));
		const setReachable = vi.fn();
		const m = new ReachabilityMonitor(fetchFn, setReachable, () => true);
		await m.probeNow();
		expect(setReachable).toHaveBeenLastCalledWith(false);
	});

	it('skips the probe while offline', async () => {
		const fetchFn = vi.fn().mockResolvedValue(okResponse(true));
		const setReachable = vi.fn();
		const m = new ReachabilityMonitor(fetchFn, setReachable, () => false);
		await m.probeNow();
		expect(fetchFn).not.toHaveBeenCalled();
		expect(setReachable).not.toHaveBeenCalled();
	});

	it('polls on the healthy cadence after a healthy probe', async () => {
		const fetchFn = vi.fn().mockResolvedValue(okResponse(true));
		const m = new ReachabilityMonitor(fetchFn, vi.fn(), () => true);
		m.start();
		await vi.advanceTimersByTimeAsync(0);
		expect(fetchFn).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(HEALTHY_INTERVAL_MS);
		expect(fetchFn).toHaveBeenCalledTimes(2);
		m.stop();
	});

	it('polls on the unhealthy cadence after a failed probe', async () => {
		const fetchFn = vi.fn().mockResolvedValue(okResponse(false));
		const m = new ReachabilityMonitor(fetchFn, vi.fn(), () => true);
		m.start();
		await vi.advanceTimersByTimeAsync(0);
		expect(fetchFn).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(UNHEALTHY_INTERVAL_MS);
		expect(fetchFn).toHaveBeenCalledTimes(2);
		m.stop();
	});

	it('reschedules onto the unhealthy cadence when probeNow flips healthy to unhealthy', async () => {
		const fetchFn = vi.fn().mockResolvedValueOnce(okResponse(true));
		const m = new ReachabilityMonitor(fetchFn, vi.fn(), () => true);
		m.start();
		await vi.advanceTimersByTimeAsync(0);
		expect(fetchFn).toHaveBeenCalledTimes(1);

		fetchFn.mockRejectedValueOnce(new TypeError('failed to fetch'));
		await m.probeNow();
		expect(fetchFn).toHaveBeenCalledTimes(2);

		await vi.advanceTimersByTimeAsync(UNHEALTHY_INTERVAL_MS);
		expect(fetchFn).toHaveBeenCalledTimes(3);
		m.stop();
	});

	it('does not start a timer from probeNow after stop()', async () => {
		const fetchFn = vi.fn().mockResolvedValueOnce(okResponse(true));
		const m = new ReachabilityMonitor(fetchFn, vi.fn(), () => true);
		m.start();
		await vi.advanceTimersByTimeAsync(0);
		m.stop();

		fetchFn.mockRejectedValueOnce(new TypeError('failed to fetch'));
		await m.probeNow();
		await vi.advanceTimersByTimeAsync(UNHEALTHY_INTERVAL_MS * 3);
		expect(fetchFn).toHaveBeenCalledTimes(2);
	});

	it('stops scheduling after stop()', async () => {
		const fetchFn = vi.fn().mockResolvedValue(okResponse(true));
		const m = new ReachabilityMonitor(fetchFn, vi.fn(), () => true);
		m.start();
		await vi.advanceTimersByTimeAsync(0);
		m.stop();
		await vi.advanceTimersByTimeAsync(HEALTHY_INTERVAL_MS * 3);
		expect(fetchFn).toHaveBeenCalledTimes(1);
	});
});
