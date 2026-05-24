/* Trust — `/trust` and `/trust/...` (public attestation inspector reskin).
 * The trust-API endpoints under `/.well-known/*` and `/attestations/*` are
 * unchanged Lambda Function URL routes — only this UI shell ports. */
import type { ShellFaceOptions } from './_shell';

export const trustRoute: ShellFaceOptions = {
	route: '/trust/{rest*}',
	routeId: 'trust',
};
