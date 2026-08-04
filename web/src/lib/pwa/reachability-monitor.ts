import { get } from 'svelte/store';
import { online, setServerReachable } from '$lib/stores/connection';

const HEALTHZ_PATH = '/api/healthz';
export const HEALTHY_INTERVAL_MS = 20000;
export const UNHEALTHY_INTERVAL_MS = 5000;

export class ReachabilityMonitor {
	private timer: ReturnType<typeof setTimeout> | undefined;
	private stopped = true;
	private healthy = true;

	constructor(
		private readonly fetchFn: typeof fetch = fetch,
		private readonly setReachable: (value: boolean) => void = setServerReachable,
		private readonly isOnline: () => boolean = () => get(online)
	) {}

	start(): void {
		if (!this.stopped) return;
		this.stopped = false;
		this.schedule(0);
	}

	stop(): void {
		this.stopped = true;
		if (this.timer) {
			clearTimeout(this.timer);
			this.timer = undefined;
		}
	}

	async probeNow(): Promise<void> {
		await this.probe();
	}

	private schedule(delay: number): void {
		if (this.stopped) return;
		this.timer = setTimeout(() => void this.tick(), delay);
	}

	private async tick(): Promise<void> {
		await this.probe();
		this.schedule(this.healthy ? HEALTHY_INTERVAL_MS : UNHEALTHY_INTERVAL_MS);
	}

	private async probe(): Promise<void> {
		if (!this.isOnline()) return;
		try {
			const resp = await this.fetchFn(HEALTHZ_PATH, { method: 'GET', credentials: 'include' });
			this.healthy = resp.ok;
		} catch {
			this.healthy = false;
		}
		this.setReachable(this.healthy);
	}
}

export const reachabilityMonitor = new ReachabilityMonitor();
