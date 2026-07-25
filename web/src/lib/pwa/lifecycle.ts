export interface PwaStatus {
	online: boolean;
	updateAvailable: boolean;
	applyingUpdate: boolean;
}

interface WorkerLike {
	postMessage(message: unknown): void;
	state?: string;
	addEventListener?(type: string, listener: () => void): void;
	removeEventListener?(type: string, listener: () => void): void;
}

interface RegistrationLike {
	waiting: WorkerLike | null;
	installing: WorkerLike | null;
	update(): Promise<unknown>;
	addEventListener(type: string, listener: () => void): void;
	removeEventListener(type: string, listener: () => void): void;
}

interface ServiceWorkerContainerLike {
	controller: unknown;
	ready?: Promise<RegistrationLike>;
	getRegistration(): Promise<RegistrationLike | undefined>;
	addEventListener(type: string, listener: () => void): void;
	removeEventListener(type: string, listener: () => void): void;
}

interface NavigatorLike {
	onLine: boolean;
	serviceWorker?: ServiceWorkerContainerLike;
}

interface WindowLike {
	addEventListener(type: string, listener: () => void): void;
	removeEventListener(type: string, listener: () => void): void;
	location: { reload(): void };
}

export class PwaLifecycle {
	private registration: RegistrationLike | undefined;
	private waitingWorker: WorkerLike | null = null;
	private reloading = false;
	private destroyed = false;
	private warnedUpdateFailure = false;
	private status: PwaStatus;

	constructor(
		private readonly onStatus: (status: PwaStatus) => void,
		private readonly navigatorRef: NavigatorLike = navigator,
		private readonly windowRef: WindowLike = window
	) {
		this.status = {
			online: navigatorRef.onLine,
			updateAvailable: false,
			applyingUpdate: false
		};
	}

	private readonly handleConnectivityChange = () => {
		this.publish({ online: this.navigatorRef.onLine });
	};

	private readonly handleControllerChange = () => {
		if (!this.status.applyingUpdate || this.reloading) return;
		this.reloading = true;
		this.windowRef.location.reload();
	};

	private readonly handleUpdateFound = () => {
		const installing = this.registration?.installing;
		if (!installing?.addEventListener) return;

		const handleStateChange = () => {
			if (installing.state !== 'installed') return;
			installing.removeEventListener?.('statechange', handleStateChange);
			this.syncWaitingWorker();
		};
		installing.addEventListener('statechange', handleStateChange);
	};

	async start(): Promise<void> {
		this.windowRef.addEventListener('online', this.handleConnectivityChange);
		this.windowRef.addEventListener('offline', this.handleConnectivityChange);
		const container = this.navigatorRef.serviceWorker;
		if (!container) {
			this.emit();
			return;
		}

		container.addEventListener('controllerchange', this.handleControllerChange);
		this.registration = await container.getRegistration();
		if (!this.registration && container.ready) {
			this.registration = await container.ready;
		}
		if (this.destroyed || !this.registration) return;

		this.registration.addEventListener('updatefound', this.handleUpdateFound);
		this.syncWaitingWorker();
	}

	async checkForUpdate(): Promise<void> {
		if (!this.registration) return;
		try {
			await this.registration.update();
			this.syncWaitingWorker();
		} catch (error) {
			if (this.warnedUpdateFailure) return;
			this.warnedUpdateFailure = true;
			console.warn('PWA update check failed', error);
		}
	}

	applyUpdate(): void {
		if (!this.waitingWorker || this.status.applyingUpdate) return;
		this.publish({ applyingUpdate: true });
		this.waitingWorker.postMessage({ type: 'SKIP_WAITING' });
	}

	destroy(): void {
		this.destroyed = true;
		this.windowRef.removeEventListener('online', this.handleConnectivityChange);
		this.windowRef.removeEventListener('offline', this.handleConnectivityChange);
		this.navigatorRef.serviceWorker?.removeEventListener(
			'controllerchange',
			this.handleControllerChange
		);
		this.registration?.removeEventListener('updatefound', this.handleUpdateFound);
	}

	private syncWaitingWorker(): void {
		this.waitingWorker = this.registration?.waiting ?? null;
		this.publish({ updateAvailable: this.waitingWorker !== null });
	}

	private publish(change: Partial<PwaStatus>): void {
		this.status = { ...this.status, ...change };
		this.emit();
	}

	private emit(): void {
		this.onStatus({ ...this.status });
	}
}
