<script lang="ts">
	import type { ScheduledProposal } from '$lib/api/scheduled';

	let {
		proposal,
		onConfirm,
		disabled = false
	}: {
		proposal: ScheduledProposal;
		onConfirm: (expectedVersion: number) => void;
		disabled?: boolean;
	} = $props();

	const humanizedSelectors = new Set([
		'FREQ',
		'INTERVAL',
		'BYDAY',
		'BYMONTHDAY',
		'COUNT',
		'UNTIL'
	]);
	const humanizedFrequencies = new Set(['DAILY', 'WEEKLY', 'MONTHLY']);

	function rruleParts(rule: string): {
		parts: Record<string, string>;
		exactRule: string;
		fullyRepresented: boolean;
	} {
		const exactRule = rule.trim().replace(/^RRULE:/i, '');
		const parts: Record<string, string> = {};
		let fullyRepresented = exactRule !== '';
		for (const item of exactRule.split(';')) {
			const separator = item.indexOf('=');
			const key = separator > 0 ? item.slice(0, separator).toUpperCase() : '';
			const value = separator > 0 ? item.slice(separator + 1) : '';
			if (!key || !value || parts[key] !== undefined || !humanizedSelectors.has(key)) {
				fullyRepresented = false;
			}
			if (key && value) parts[key] = value;
		}
		if (!parts.FREQ) fullyRepresented = false;
		if (parts.FREQ && !humanizedFrequencies.has(parts.FREQ)) fullyRepresented = false;
		if (parts.BYDAY && !parts.BYDAY.split(',').every((day) => /^(MO|TU|WE|TH|FR|SA|SU)$/.test(day))) {
			fullyRepresented = false;
		}
		if (
			parts.BYMONTHDAY &&
			!parts.BYMONTHDAY.split(',').every((day) => {
				const parsed = Number(day);
				return /^-?\d+$/.test(day) && parsed !== 0 && parsed >= -31 && parsed <= 31;
			})
		) {
			fullyRepresented = false;
		}
		for (const key of ['INTERVAL', 'COUNT']) {
			if (parts[key] && !/^[1-9]\d*$/.test(parts[key])) fullyRepresented = false;
		}
		return { parts, exactRule, fullyRepresented };
	}

	function frequencyLabel(frequency: string, interval: number, rule: string): string {
		const labels: Record<string, [string, string]> = {
			DAILY: ['day', 'days'],
			WEEKLY: ['week', 'weeks'],
			MONTHLY: ['month', 'months']
		};
		const label = labels[frequency];
		if (!label) return rule ? `Custom recurrence (${rule})` : 'Recurring';
		return interval === 1 ? `Every ${label[0]}` : `Every ${interval} ${label[1]}`;
	}

	function untilLabel(value: string, timezone: string): string {
		const compact = value.match(/^(\d{4})(\d{2})(\d{2})(?:T(\d{2})(\d{2})(\d{2})Z)?$/);
		const date = compact
			? new Date(
					compact[4]
						? `${compact[1]}-${compact[2]}-${compact[3]}T${compact[4]}:${compact[5]}:${compact[6]}Z`
						: `${compact[1]}-${compact[2]}-${compact[3]}T00:00:00Z`
				)
			: new Date(value);
		if (Number.isNaN(date.getTime())) return value;
		return new Intl.DateTimeFormat(undefined, {
			dateStyle: 'medium',
			timeZone: timezone
		}).format(date);
	}

	function exactRecurrence(starts: string | undefined, rule: string): string {
		return [
			'Custom recurrence',
			starts ? `DTSTART: ${starts}` : '',
			`RRULE: ${rule.trim().replace(/^RRULE:/i, '')}`
		]
			.filter(Boolean)
			.join(' · ');
	}

	function humanizesSelectorCombination(parts: Record<string, string>): boolean {
		if (parts.FREQ === 'DAILY') return !parts.BYDAY && !parts.BYMONTHDAY;
		if (parts.FREQ === 'WEEKLY') return Boolean(parts.BYDAY) && !parts.BYMONTHDAY;
		if (parts.FREQ === 'MONTHLY') return Boolean(parts.BYMONTHDAY) && !parts.BYDAY;
		return false;
	}

	function cadence(): string {
		const schedule = proposal.schedule;
		const at = schedule.at ?? schedule.At;
		const starts = schedule.dtStart ?? schedule.DTStart;
		const rule = schedule.rrule ?? schedule.RRULE ?? '';
		const timezone = schedule.timezone ?? schedule.Timezone ?? proposal.timezone;
		if (at) {
			return `Once · ${new Intl.DateTimeFormat(undefined, {
				dateStyle: 'medium',
				timeStyle: 'short',
				timeZone: timezone
			}).format(new Date(at))}`;
		}
		const parsed = rruleParts(rule);
		if (!parsed.fullyRepresented) {
			return exactRecurrence(starts, parsed.exactRule || rule);
		}
		const parts = parsed.parts;
		if (!humanizesSelectorCombination(parts) || parts.UNTIL?.includes('T')) {
			return exactRecurrence(starts, parsed.exactRule);
		}
		const parsedInterval = Number(parts.INTERVAL ?? '1');
		const interval =
			Number.isSafeInteger(parsedInterval) && parsedInterval > 0 ? parsedInterval : 1;
		const frequency = frequencyLabel(parts.FREQ ?? '', interval, rule);
		const days = (parts.BYDAY ?? '')
			.replaceAll('MO', 'Mon')
			.replaceAll('TU', 'Tue')
			.replaceAll('WE', 'Wed')
			.replaceAll('TH', 'Thu')
			.replaceAll('FR', 'Fri')
			.replaceAll('SA', 'Sat')
			.replaceAll('SU', 'Sun')
			.replaceAll(',', ', ');
		const monthDays = parts.BYMONTHDAY ? `on day ${parts.BYMONTHDAY.replaceAll(',', ', ')}` : '';
		const time = starts
			? new Intl.DateTimeFormat(undefined, {
					hour: 'numeric',
					minute: '2-digit',
					timeZone: timezone
				}).format(new Date(starts))
			: '';
		const count = parts.COUNT ? `for ${parts.COUNT} runs` : '';
		const until = parts.UNTIL ? `until ${untilLabel(parts.UNTIL, timezone)}` : '';
		return [frequency, days, monthDays, time ? `at ${time}` : '', count, until]
			.filter(Boolean)
			.join(' · ');
	}

	function readableTool(tool: string): string {
		return tool
			.replace(/__/g, ' ')
			.replace(/_/g, ' ')
			.replace(/\b\w/g, (letter) => letter.toUpperCase());
	}

	function initialBehavior(): string {
		if (proposal.initialRun === 'preview') return 'Run a preview after confirmation';
		if (proposal.initialRun === 'baseline') return 'Establish a quiet baseline first';
		return 'Wait for the first scheduled time';
	}
</script>

<section class="proposal" aria-labelledby="proposal-name">
	<span class="eyebrow">Ready to schedule</span>
	<h2 id="proposal-name">{proposal.name}</h2>
	<dl>
		<div><dt>Cadence</dt><dd>{cadence()}</dd></div>
		<div><dt>Timezone</dt><dd>{proposal.timezone}</dd></div>
		<div>
			<dt>Delivery</dt>
			<dd>{proposal.deliveryPolicy === 'on_change' ? 'Only when something changes' : 'After every run'}</dd>
		</div>
		<div><dt>First run</dt><dd>{initialBehavior()}</dd></div>
		<div>
			<dt>Integrations</dt>
			<dd>
				{proposal.authorizedTools.length
					? proposal.authorizedTools.map(readableTool).join(', ')
					: 'None — this reminder runs without integrations'}
			</dd>
		</div>
	</dl>
	<details>
		<summary>View final instruction</summary>
		<p>{proposal.compiledPrompt}</p>
	</details>
	<button disabled={disabled} onclick={() => onConfirm(proposal.version)}>Schedule task</button>
</section>

<style>
	.proposal {
		padding: 22px;
		border: 1px solid color-mix(in srgb, var(--accent) 45%, var(--border));
		border-radius: calc(var(--radius) + 4px);
		background: #eef5f3;
	}
	.eyebrow {
		color: var(--accent);
		font: 600 0.7rem/1 ui-monospace, SFMono-Regular, Consolas, monospace;
		letter-spacing: 0.08em;
		text-transform: uppercase;
	}
	h2 { margin: 6px 0 18px; font-size: 1.2rem; }
	dl { display: grid; gap: 10px; margin: 0 0 16px; }
	dl div { display: grid; grid-template-columns: 110px minmax(0, 1fr); gap: 12px; }
	dt { color: var(--text-muted); }
	dd { margin: 0; font-weight: 550; }
	details { border-top: 1px solid color-mix(in srgb, var(--accent) 25%, transparent); padding-top: 12px; }
	summary { cursor: pointer; color: var(--text-muted); }
	details p {
		margin-bottom: 0;
		white-space: pre-wrap;
		font: 0.83rem/1.5 ui-monospace, SFMono-Regular, Consolas, monospace;
	}
	button {
		display: block;
		width: 100%;
		margin-top: 18px;
		padding: 10px 16px;
		border: 1px solid var(--accent);
		border-radius: var(--radius);
		background: var(--accent);
		color: #fff;
		font: inherit;
		font-weight: 650;
		cursor: pointer;
	}
	button:disabled { opacity: 0.55; cursor: not-allowed; }
	button:focus-visible, summary:focus-visible { outline: 3px solid color-mix(in srgb, var(--accent) 35%, transparent); outline-offset: 2px; }
	@media (max-width: 520px) {
		.proposal { padding: 17px; }
		dl div { grid-template-columns: 1fr; gap: 1px; }
	}
</style>
