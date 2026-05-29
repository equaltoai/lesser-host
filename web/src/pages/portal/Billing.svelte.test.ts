/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

const source = readFileSync(join(process.cwd(), 'src/pages/portal/Billing.svelte'), 'utf8');
const apiBillingSource = readFileSync(join(process.cwd(), 'src/lib/api/portalBilling.ts'), 'utf8');
const apiUsageSource = readFileSync(join(process.cwd(), 'src/lib/api/portalUsage.ts'), 'utf8');

describe('M11 Billing UI — corrective rework', () => {
	it('preserves M0.2 page title fix — renders Billing h1/title, not Portal Dashboard', () => {
		const scriptEnd = source.indexOf('</script>');
		expect(scriptEnd).toBeGreaterThan(0);
		const template = source.slice(scriptEnd);
		expect(template).toContain('Where the money goes.');
		expect(template).toContain('Cost &amp; billing');
	});

	it('renders four metric cards (MTD, Projected EOM, Per active user, Per federated post)', () => {
		const scriptEnd = source.indexOf('</script>');
		const template = source.slice(scriptEnd);
		expect(template).toContain('Per active user');
		expect(template).toContain('Per federated post');
		expect(template).toContain('MTD');
		expect(template).toContain('Projected EOM');
	});

	it('does NOT use hardcoded unavailable placeholder value="—" for metric denominators', () => {
		// The corrective rework replaced hardcoded '—' placeholders with
		// real computed values (perActiveUser, perFederatedPost). The only
		// '—' allowed is the $— display when denominator is zero.
		const scriptEnd = source.indexOf('</script>');
		const template = source.slice(scriptEnd);
		// Check that value="—" (hardcoded em-dash) does not appear in metric cards
		const metricSection = template.slice(
			template.indexOf('billing-metrics'),
			template.indexOf('billing-metrics') + 600
		);
		// No bare value="—" in metric cards
		expect(metricSection).not.toContain('value="—"');
	});

	it('deletes BUDGET_FALLBACKS — no hardcoded budget lookup table', () => {
		// Check only the script and template sections (after the HTML comment close -->)
		const afterComment = source.slice(source.indexOf('-->') + 3);
		expect(afterComment).not.toContain('BUDGET_FALLBACKS');
	});

	it('deletes inferBudget — no hardcoded budget fallback function', () => {
		// Check only the script and template sections (after the HTML comment close -->)
		const afterComment = source.slice(source.indexOf('-->') + 3);
		expect(afterComment).not.toContain('inferBudget');
	});

	it('imports and calls portalGetBudgetMonth for real per-instance budgets', () => {
		expect(source).toContain('portalGetBudgetMonth');
		expect(source).toContain("portalGetBudgetMonth(token, inst.slug, currentMonth)");
	});

	it('imports and calls portalGetInstanceActivity for post/status denominator', () => {
		expect(source).toContain('portalGetInstanceActivity');
		expect(source).toContain("portalGetInstanceActivity(token, inst.slug)");
	});

	it('extracts UniqueUsers from cost telemetry for Per active user denominator', () => {
		expect(source).toContain('UniqueUsers');
		expect(source).toContain('extractMaxDailyUniqueUsers');
		expect(source).toContain('metric.metric_name');
	});

	it('uses portalGetInstanceActivity statuses (not Requests) for Per federated post denominator', () => {
		// Must NOT use Requests as a proxy for post count
		const scriptSection = source.slice(0, source.indexOf('</script>'));
		expect(source).toContain('totalStatuses');
		expect(source).toContain('perFederatedPost');
		// Ensure Requests is NOT used as a post denominator proxy
		expect(scriptSection).not.toContain('Requests');
	});

	it('is CSP-safe — no inline style attributes', () => {
		const template = source.slice(source.indexOf('</script>'));
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
		const template = source.slice(source.indexOf('</script>'));
		expect(template).toContain('paymentMethodResponse');
		expect(template).not.toContain('provider_customer_id');
		expect(template).not.toContain('provider_checkout_session_id');
		expect(template).not.toContain('provider_payment_intent_id');
		expect(template).not.toContain('account_id');
	});

	it('renders invoice list from safe DTO without internal fields', () => {
		const template = source.slice(source.indexOf('</script>'));
		expect(template).toContain('invoicesResponse');
		expect(template).toContain('period_start');
		expect(template).toContain('amount_due');
	});

	it('still a UI-focused milestone — no Go package declarations smuggled in', () => {
		expect(source).not.toContain('package controlplane');
		expect(source).not.toContain('package payments');
		expect(source).not.toContain('package store');
	});
});

describe('portalUsage API — M11 budget and activity exports', () => {
	it('exports portalGetBudgetMonth for per-instance monthly budget fetch', () => {
		expect(apiUsageSource).toContain('portalGetBudgetMonth');
		expect(apiUsageSource).toContain('/api/v1/portal/instances/');
	});

	it('exports portalGetInstanceActivity for owner-scoped activity bridge', () => {
		expect(apiUsageSource).toContain('portalGetInstanceActivity');
		expect(apiUsageSource).toContain('/api/v1/portal/instances/');
		expect(apiUsageSource).toContain('/activity');
	});

	it('PortalInstanceActivityResponse DTO has safe fields only (no raw keys)', () => {
		expect(apiUsageSource).toContain('PortalInstanceActivityResponse');
		expect(apiUsageSource).toContain('instance_slug');
		expect(apiUsageSource).toContain('statuses');
		expect(apiUsageSource).toContain('weeks');
		expect(apiUsageSource).toContain('month');
		// No raw instance keys, account IDs, provider secrets
		expect(apiUsageSource).not.toContain('raw_key');
		expect(apiUsageSource).not.toContain('secret_arn');
	});

	it('BudgetMonthResponse DTO has credits fields (included_credits, used_credits)', () => {
		expect(apiUsageSource).toContain('BudgetMonthResponse');
		expect(apiUsageSource).toContain('included_credits');
		expect(apiUsageSource).toContain('used_credits');
	});
});

describe('portalBilling API — M12 invoice and payment-method DTOs', () => {
	it('exports portalListInvoices function', () => {
		expect(apiBillingSource).toContain('portalListInvoices');
		expect(apiBillingSource).toContain('/api/v1/portal/billing/invoices');
	});

	it('exports portalGetPaymentMethod function', () => {
		expect(apiBillingSource).toContain('portalGetPaymentMethod');
		expect(apiBillingSource).toContain('/api/v1/portal/billing/payment-method');
	});

	it('InvoiceSummary DTO has safe fields only', () => {
		expect(apiBillingSource).toContain('period_start');
		expect(apiBillingSource).toContain('period_end');
		expect(apiBillingSource).toContain('amount_due');
		expect(apiBillingSource).toContain('hosted_invoice_url');
		expect(apiBillingSource).toContain('invoice_pdf_url');
		expect(apiBillingSource).not.toContain('stripe_invoice');
		expect(apiBillingSource).not.toContain('raw_invoice');
	});

	it('PaymentMethodSafe DTO has masked fields only', () => {
		expect(apiBillingSource).toContain('PaymentMethodSafe');
		expect(apiBillingSource).toContain('brand');
		expect(apiBillingSource).toContain('last4');
		expect(apiBillingSource).toContain('exp_month');
		expect(apiBillingSource).toContain('exp_year');
		expect(apiBillingSource).toContain('status');
		expect(apiBillingSource).not.toContain('pan');
		expect(apiBillingSource).not.toContain('cvv');
		expect(apiBillingSource).not.toContain('full_token');
	});

	it('GetPaymentMethodResponse has nullable payment_method', () => {
		expect(apiBillingSource).toContain('payment_method: PaymentMethodSafe | null');
	});
});
