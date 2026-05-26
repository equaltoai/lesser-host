<!--
@component
Billing — customer billing page (credits purchases + payment methods).

M1.4 re-skin: renders on the new shell (greater-components `PageFrame` +
`PageTitle` + `Panel`) with DS-token spacing. Endpoint shapes, checkout
redirect flow (no third-party scripts — Stripe Checkout redirect only),
and webhook reconciliation hand-off are preserved byte-for-byte from the
pre-M1 implementation; only the visual layer changes.

Posture invariants preserved:
  - Strict-no-inline-CSP safe: no inline scripts/styles; no third-party
    Stripe.js or Embed; redirect-only setup flow preserved.
  - Multi-tenant isolation: consumes only per-owner portal billing
    endpoints; no cross-tenant reads.
  - No raw payment data is rendered (only provider IDs / last4 /
    redacted brand strings).

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M1.4
Issue: equaltoai/lesser-host#413
-->

<script lang="ts">
	import { onMount } from 'svelte';

	import { type ApiError, isSafeRedirectUrl } from 'src/lib/api/http';
	import type {
		ListCreditPurchasesResponse,
		ListPaymentMethodsResponse,
	} from 'src/lib/api/portalBilling';
	import {
		portalCreatePaymentMethodCheckout,
		portalListCreditPurchases,
		portalListPaymentMethods,
	} from 'src/lib/api/portalBilling';
	import { logout } from 'src/lib/auth/logout';
	import { linkProps, navigate } from 'src/lib/router';
	import { Alert, Badge, Button, CopyButton, Link, Spinner, Text } from 'src/lib/ui';
	import { PageFrame, PageTitle, Panel } from 'src/lib/shell';

	let { token } = $props<{ token: string }>();

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);

	let purchases = $state<ListCreditPurchasesResponse | null>(null);
	let methods = $state<ListPaymentMethodsResponse | null>(null);

	let checkoutNotice = $state<string | null>(null);
	let addPaymentMethodLoading = $state(false);
	let addPaymentMethodError = $state<string | null>(null);

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	function statusBadge(status: string): {
		variant: 'outlined' | 'filled';
		color: 'success' | 'warning' | 'error' | 'gray';
	} {
		const s = (status || '').toLowerCase();
		if (s === 'paid' || s === 'active') return { variant: 'filled', color: 'success' };
		if (s === 'pending') return { variant: 'outlined', color: 'warning' };
		if (s === 'error' || s === 'failed') return { variant: 'filled', color: 'error' };
		return { variant: 'outlined', color: 'gray' };
	}

	function initCheckoutNotice() {
		const qs = new URLSearchParams(window.location.search);
		if (qs.get('success') === '1') {
			checkoutNotice =
				'Checkout completed. It may take a moment for credits/payment method to appear after webhook reconciliation.';
		} else if (qs.get('canceled') === '1') {
			checkoutNotice = 'Checkout canceled.';
		}

		if (checkoutNotice) {
			history.replaceState({}, '', window.location.pathname);
		}
	}

	async function loadAll() {
		errorMessage = null;
		purchases = null;
		methods = null;

		loading = true;
		try {
			const [p, m] = await Promise.all([
				portalListCreditPurchases(token),
				portalListPaymentMethods(token),
			]);
			purchases = p;
			methods = m;
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			errorMessage = formatError(err);
		} finally {
			loading = false;
		}
	}

	async function addPaymentMethod() {
		addPaymentMethodError = null;
		addPaymentMethodLoading = true;
		try {
			const res = await portalCreatePaymentMethodCheckout(token);
			if (!isSafeRedirectUrl(res.checkout_url)) {
				addPaymentMethodError = 'Invalid checkout URL received from server.';
				return;
			}
			window.location.assign(res.checkout_url);
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			addPaymentMethodError = formatError(err);
		} finally {
			addPaymentMethodLoading = false;
		}
	}

	onMount(() => {
		initCheckoutNotice();
		void loadAll();
	});
</script>

<PageFrame width="default">
	{#snippet header()}
		<PageTitle
			eyebrow="Billing"
			title="Billing"
			description="Credit purchases and payment methods."
		>
			{#snippet actions()}
				<Button variant="outline" onclick={() => void loadAll()} disabled={loading}>Refresh</Button>
				<Link {...linkProps('/portal')} variant="ghost">Back</Link>
			{/snippet}
		</PageTitle>
	{/snippet}

	{#if checkoutNotice}
		<Alert variant="info" title="Checkout">{checkoutNotice}</Alert>
	{/if}

	{#if loading}
		<div class="billing__loading">
			<Spinner size="md" />
			<Text>Loading…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Failed to load billing">{errorMessage}</Alert>
	{:else}
		<Panel title="Payment methods" headerLevel={2}>
			<Text size="sm" color="secondary">
				Redirect-only setup flow (no third-party scripts).
			</Text>

			<div class="billing__row">
				<Button
					variant="solid"
					onclick={() => void addPaymentMethod()}
					disabled={addPaymentMethodLoading}
				>
					Add / replace payment method
				</Button>
				{#if addPaymentMethodLoading}
					<div class="billing__loading-inline">
						<Spinner size="sm" />
						<Text size="sm">Redirecting…</Text>
					</div>
				{/if}
			</div>
			{#if addPaymentMethodError}
				<Alert variant="error" title="Setup failed">{addPaymentMethodError}</Alert>
			{/if}

			{#if methods && methods.methods.length === 0}
				<Alert variant="info" title="No payment methods">
					<Text size="sm">No payment method on file.</Text>
				</Alert>
			{:else if methods}
				<div class="billing__list">
					{#each methods.methods as method (method.id)}
						<div class="billing__list-row">
							<div class="billing__list-main">
								<Text size="sm">
									{@const badge = statusBadge(method.status)}
									<Badge variant={badge.variant} color={badge.color} size="sm">{method.status}</Badge>
									<span class="billing__mono">{method.brand || method.type || 'method'}</span>
									{#if method.last4}
										<span class="billing__mono">•••• {method.last4}</span>
									{/if}
									{#if methods.default_payment_method_id && methods.default_payment_method_id === method.id}
										<Badge variant="outlined" color="gray" size="sm">default</Badge>
									{/if}
								</Text>
								<Text size="sm" color="secondary">
									expires {method.exp_month || '—'}/{method.exp_year || '—'} · provider
									<span class="billing__mono">{method.provider}</span>
								</Text>
							</div>
							<div class="billing__list-meta">
								<CopyButton size="sm" text={method.id} />
								<Text size="sm" color="secondary">
									<span class="billing__mono">{method.created_at}</span>
								</Text>
							</div>
						</div>
					{/each}
				</div>
			{:else}
				<Alert variant="warning" title="No data">
					<Text size="sm">No response from payment method endpoints.</Text>
				</Alert>
			{/if}
		</Panel>

		<Panel title="Credit purchases" headerLevel={2}>
			<Text size="sm" color="secondary">
				Purchases are stored server-side; receipts may appear after payment completes.
			</Text>

			{#if purchases && purchases.purchases.length === 0}
				<Alert variant="info" title="No purchases">
					<Text size="sm">No credit purchases yet.</Text>
				</Alert>
			{:else if purchases}
				<div class="billing__list">
					{#each purchases.purchases.slice(0, 50) as purchase (purchase.id)}
						<div class="billing__list-row">
							<div class="billing__list-main">
								<Text size="sm">
									{@const badge = statusBadge(purchase.status)}
									<Badge variant={badge.variant} color={badge.color} size="sm">{purchase.status}</Badge>
									<span class="billing__mono">{purchase.instance_slug}</span>
									· <span class="billing__mono">{purchase.month}</span>
									· credits <span class="billing__mono">{String(purchase.credits)}</span>
								</Text>
								<Text size="sm" color="secondary">
									amount <span class="billing__mono">{String(purchase.amount_cents)}</span>
									<span class="billing__mono">{purchase.currency}</span>
									· provider <span class="billing__mono">{purchase.provider}</span>
								</Text>
								{#if purchase.receipt_url}
									<Text size="sm" color="secondary">
										receipt
										<a
											class="billing__link"
											href={purchase.receipt_url}
											target="_blank"
											rel="noopener noreferrer"
										>
											{purchase.receipt_url}
										</a>
									</Text>
								{/if}
								{#if purchase.request_id}
									<Text size="sm" color="secondary">
										request <span class="billing__mono">{purchase.request_id}</span>
									</Text>
								{/if}
							</div>
							<div class="billing__list-meta">
								<CopyButton size="sm" text={purchase.id} />
								<Text size="sm" color="secondary">
									<span class="billing__mono">{purchase.created_at}</span>
								</Text>
							</div>
						</div>
					{/each}
				</div>
				{#if purchases.purchases.length > 50}
					<Text size="sm" color="secondary">Showing first 50 purchases.</Text>
				{/if}
			{:else}
				<Alert variant="warning" title="No data">
					<Text size="sm">No response from credit purchase endpoints.</Text>
				</Alert>
			{/if}
		</Panel>
	{/if}
</PageFrame>

<style>
	.billing__loading {
		display: flex;
		gap: var(--ds-space-3);
		align-items: center;
	}

	.billing__row {
		display: flex;
		gap: var(--ds-space-3);
		align-items: center;
		margin-top: var(--ds-space-4);
		flex-wrap: wrap;
	}

	.billing__loading-inline {
		display: flex;
		gap: var(--ds-space-2);
		align-items: center;
	}

	.billing__list {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-3);
		margin-top: var(--ds-space-4);
	}

	.billing__list-row {
		display: flex;
		gap: var(--ds-space-4);
		justify-content: space-between;
		flex-wrap: wrap;
		padding: var(--ds-space-3);
		border: 1px solid var(--ds-border-subtle, var(--gr-color-border-subtle));
		border-radius: var(--ds-radius-md, var(--gr-radius-md));
		background: var(--ds-bg-raised, var(--gr-color-surface));
	}

	.billing__list-main {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-1);
	}

	.billing__list-meta {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-2);
		align-items: flex-end;
	}

	.billing__mono {
		font-family: var(--ds-font-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace);
	}

	.billing__link {
		word-break: break-all;
		color: var(--ds-action-link, var(--gr-color-primary-foreground));
		text-decoration: underline;
		text-underline-offset: 2px;
	}
</style>
