import { fetchJson, jsonRequest } from './http';

export interface CreditPurchase {
	id: string;
	username: string;
	instance_slug: string;
	month: string;
	credits: number;
	amount_cents: number;
	currency: string;
	provider: string;
	provider_checkout_session_id?: string;
	provider_payment_intent_id?: string;
	provider_customer_id?: string;
	receipt_url?: string;
	status: string;
	request_id?: string;
	created_at: string;
	updated_at: string;
	paid_at?: string;
}

export interface CreditsCheckoutResponse {
	purchase: CreditPurchase;
	checkout_url: string;
}

export interface ListCreditPurchasesResponse {
	purchases: CreditPurchase[];
	count: number;
}

export interface PaymentMethod {
	username: string;
	provider: string;
	id: string;
	type?: string;
	brand?: string;
	last4?: string;
	exp_month?: number;
	exp_year?: number;
	status: string;
	created_at: string;
	updated_at: string;
}

export interface PaymentMethodCheckoutResponse {
	checkout_url: string;
}

export interface ListPaymentMethodsResponse {
	default_payment_method_id?: string;
	methods: PaymentMethod[];
	count: number;
}

export function portalCreateCreditsCheckout(
	token: string,
	input: { instance_slug: string; credits: number; month?: string },
): Promise<CreditsCheckoutResponse> {
	const req = jsonRequest(input);
	return fetchJson<CreditsCheckoutResponse>('/api/v1/portal/billing/credits/checkout', {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
			...req.headers,
		},
		body: req.body,
	});
}

export function portalListCreditPurchases(token: string): Promise<ListCreditPurchasesResponse> {
	return fetchJson<ListCreditPurchasesResponse>('/api/v1/portal/billing/credits/purchases', {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function portalCreatePaymentMethodCheckout(token: string): Promise<PaymentMethodCheckoutResponse> {
	return fetchJson<PaymentMethodCheckoutResponse>('/api/v1/portal/billing/payment-method/checkout', {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function portalListPaymentMethods(token: string): Promise<ListPaymentMethodsResponse> {
	return fetchJson<ListPaymentMethodsResponse>('/api/v1/portal/billing/payment-methods', {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

