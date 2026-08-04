import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { ReachabilityMonitor, HEALTHY_INTERVAL_MS, UNHEALTHY_INTERVAL_MS } from './reachability-monitor';

function okResponse(ok: boolean) {
	return { ok, status: ok ? 200 : 503 } as Response;
}

function statusResponse(status: number) {
	return { ok: status >= 200 && status < 300, status } as Response;
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

	it('marks unreachable on a gateway-down (503) probe', async () => {
		const fetchFn = vi.fn().mockResolvedValue(statusResponse(503));
		const setReachable = vi.fn();
		const m = new ReachabilityMonitor(fetchFn, setReachable, () => true);
		await m.probeNow();
		expect(setReachable).toHaveBeenLastCalledWith(false);
	});

	it('marks reachable on a non-2xx probe that is not gateway-down (404 = server answered)', async () => {
		const fetchFn = vi.fn().mockResolvedValue(statusResponse(404));
		const setReachable = vi.fn();
		const m = new ReachabilityMonitor(fetchFn, setReachable, () => true);
		await m.probeNow();
		expect(setReachable).toHaveBeenLastCalledWith(true);
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

	it('cancels a stale timer when tick() and a concurrent probeNow() both reschedule', async () => {
		let resolveSecondFetch: (response: Response) => void = () => {};
		const secondFetch = new Promise<Response>((resolve) => {
			resolveSecondFetch = resolve;
		});
		const fetchFn = vi
			.fn()
			.mockResolvedValueOnce(okResponse(true))
			.mockReturnValueOnce(secondFetch)
			.mockRejectedValueOnce(new TypeError('failed to fetch'));
		const m = new ReachabilityMonitor(fetchFn, vi.fn(), () => true);

		m.start();
		await vi.advanceTimersByTimeAsync(0);
		expect(fetchFn).toHaveBeenCalledTimes(1);

		// Fire the 20s healthy timer; tick() calls fetchFn and suspends on the
		// still-pending second fetch before it gets to reschedule itself.
		await vi.advanceTimersByTimeAsync(HEALTHY_INTERVAL_MS);
		expect(fetchFn).toHaveBeenCalledTimes(2);

		// Concurrent probeNow() call (as the client/stream would issue on a
		// network failure) flips to unhealthy and reschedules at 5s while
		// tick() is still suspended.
		await m.probeNow();
		expect(fetchFn).toHaveBeenCalledTimes(3);

		// Let the suspended tick() resume: it resolves healthy and reschedules
		// onto the 20s cadence, which must not leak the 5s timer probeNow armed.
		resolveSecondFetch(okResponse(true));
		await vi.advanceTimersByTimeAsync(0);

		fetchFn.mockResolvedValue(okResponse(true));
		await vi.advanceTimersByTimeAsync(UNHEALTHY_INTERVAL_MS);

		// The 5s timer probeNow armed must have been cancelled by tick()'s
		// reschedule, so no extra probe fires at the +5s mark.
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
