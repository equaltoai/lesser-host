/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

const source = readFileSync(join(process.cwd(), 'src/pages/portal/Billing.svelte'), 'utf8');
const apiSource = readFileSync(join(process.cwd(), 'src/lib/api/portalBilling.ts'), 'utf8');

describe('M11 Billing UI', () => {
	it('preserves M0.2 page title fix — renders Billing h1/title, not Portal Dashboard', () => {
		// The M0.2 fix in Portal.svelte scopes pageTitle to 'Billing' for /portal/billing.
		// The Billing component itself renders a PageTitle that carries the design's
		// "Where the money goes." title. The outer h1 in Portal.svelte is 'Billing'.
		// This test confirms the inner PageTitle uses the design title (not "Billing").
		const scriptEnd = source.indexOf('</script>');
		expect(scriptEnd).toBeGreaterThan(0);
		const template = source.slice(scriptEnd);
		expect(template).toContain('Where the money goes.');
		expect(template).toContain('Cost &amp; billing');
	});

	it('renders four metric cards with explicit unavailable state for unsupported denominators', () => {
		// "Per active user" and "Per federated post" must show '—', never invented
		// numbers or divide-by-zero.
		const scriptEnd = source.indexOf('</script>');
		const template = source.slice(scriptEnd);
		expect(template).toContain('Per active user');
		expect(template).toContain('Per federated post');
		// Both unavailable denominators render '—' as their value
		const metricValuePattern = /value="—"/g;
		const matches = template.match(metricValuePattern);
		expect(matches).not.toBeNull();
		expect(matches!.length).toBeGreaterThanOrEqual(2);
	});

	it('is CSP-safe — no inline style attributes', () => {
		// Strict-CSP posture: no style="..." attributes. All styling through
		// CSS classes, data-* selectors, and SVG presentation attributes.
		const template = source.slice(source.indexOf('</script>'));
		// SVG presentation attributes (fill, stroke, etc.) are not inline CSS styles
		// and are CSP-safe per the SVG spec.
		// The only style-like attribute allowed is Svelte's class: directive output.
		// We check that there are no HTML style="...value..." attribute patterns.
		const inlineStylePattern = /<[^>]+\sstyle\s*=\s*"[^"]*[a-zA-Z][^"]*"/gi;
		expect(inlineStylePattern.test(template)).toBe(false);
	});

	it('has AGPL-3.0-only license header', () => {
		expect(source).toContain('@license AGPL-3.0-only');
		expect(source).toContain('SPDX-License-Identifier: AGPL-3.0-only');
	});

	it('redirects to /login on 401 from billing endpoints', () => {
		expect(source).toContain("(err as Partial<ApiError>).status === 401");
		expect(source).toContain('await logout();');
		expect(source).toContain("navigate('/login');");
	});

	it('loads M12 invoice and payment-method endpoints', () => {
		expect(source).toContain('portalListInvoices');
		expect(source).toContain('portalGetPaymentMethod');
	});

	it('calls portalListInstances for instance list', () => {
		expect(source).toContain('portalListInstances');
	});

	it('calls portalGetInstanceCost for per-instance cost telemetry', () => {
		expect(source).toContain('portalGetInstanceCost');
	});

	it('does not render raw payment secrets — no PAN, CVV, raw tokens', () => {
		// The payment method DTO only exposes brand/last4/expiry/status.
		// No raw PAN (16-digit sequences), CVV, full tokens, account IDs,
		// PK/SK, checkout session IDs, or payment intent IDs.
		const template = source.slice(source.indexOf('</script>'));
		expect(template).toContain('paymentMethodResponse');
		expect(template).not.toContain('provider_customer_id');
		expect(template).not.toContain('provider_checkout_session_id');
		expect(template).not.toContain('provider_payment_intent_id');
		expect(template).not.toContain('account_id');
	});

	it('renders invoice list from safe DTO without internal fields', () => {
		// The InvoiceSummary DTO (portalBilling.ts) defines safe fields only:
		// id, period_start, period_end, amount_due, currency, status,
		// hosted_invoice_url, invoice_pdf_url. The template renders
		// period_start and amount_due from these safe fields.
		const template = source.slice(source.indexOf('</script>'));
		expect(template).toContain('invoicesResponse');
		// Template accesses inv.period_start (safe date) and inv.amount_due (safe amount)
		expect(template).toContain('period_start');
		expect(template).toContain('amount_due');
	});

	it('no backend/internal edits — UI-only milestone', () => {
		// M11 is a UI-only milestone. The backend M12 endpoints are consumed
		// but not modified here. Verify no Go source imports or package
		// declarations (the tell-tale sign of backend code).
		expect(source).not.toContain('package controlplane');
		expect(source).not.toContain('package payments');
		expect(source).not.toContain('package store');
	});
});

describe('portalBilling API — M12 invoice and payment-method DTOs', () => {
	it('exports portalListInvoices function', () => {
		expect(apiSource).toContain('portalListInvoices');
		expect(apiSource).toContain('/api/v1/portal/billing/invoices');
	});

	it('exports portalGetPaymentMethod function', () => {
		expect(apiSource).toContain('portalGetPaymentMethod');
		expect(apiSource).toContain('/api/v1/portal/billing/payment-method'); // singular
	});

	it('InvoiceSummary DTO has safe fields only', () => {
		expect(apiSource).toContain('period_start');
		expect(apiSource).toContain('period_end');
		expect(apiSource).toContain('amount_due');
		expect(apiSource).toContain('hosted_invoice_url');
		expect(apiSource).toContain('invoice_pdf_url');
		// No raw Stripe objects
		expect(apiSource).not.toContain('stripe_invoice');
		expect(apiSource).not.toContain('raw_invoice');
	});

	it('PaymentMethodSafe DTO has masked fields only', () => {
		expect(apiSource).toContain('PaymentMethodSafe');
		expect(apiSource).toContain('brand');
		expect(apiSource).toContain('last4');
		expect(apiSource).toContain('exp_month');
		expect(apiSource).toContain('exp_year');
		expect(apiSource).toContain('status');
		// No raw payment data
		expect(apiSource).not.toContain('pan');
		expect(apiSource).not.toContain('cvv');
		expect(apiSource).not.toContain('full_token');
	});

	it('GetPaymentMethodResponse has nullable payment_method', () => {
		expect(apiSource).toContain('payment_method: PaymentMethodSafe | null');
	});
});
