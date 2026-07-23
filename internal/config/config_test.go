package config

import (
	"slices"
	"testing"
)

const (
	testMicroVMControllerEndpoint = "https://controller.example/microvms"
	testMicroVMAuthTokenParam     = "/lesser-host/lab/hosted-genesis/microvm/auth-token"
	testMicroVMImageRef           = "image-ref"
	testMicroVMIngressOne         = "ingress-1"
	testMicroVMIngressTwo         = "ingress-2"
	testMicroVMEgressOne          = "egress-1"
	testMicroVMEgressTwo          = "egress-2"
)

func assertEqual[T comparable](t *testing.T, label string, got T, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: expected %#v, got %#v", label, want, got)
	}
}

func assertTrue(t *testing.T, label string, got bool) {
	t.Helper()
	if !got {
		t.Fatalf("%s: expected true", label)
	}
}

func assertStringSliceEqual(t *testing.T, label string, got []string, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s: expected %#v, got %#v", label, want, got)
	}
}

func TestEnvHelpers(t *testing.T) {
	t.Setenv("X", "  hi ")
	if got := envString("X"); got != "hi" {
		t.Fatalf("envString: expected %q, got %q", "hi", got)
	}

	t.Setenv("BOOL", "YES")
	if !envBoolOn("BOOL") {
		t.Fatalf("envBoolOn: expected true")
	}
	t.Setenv("BOOL", "0")
	if envBoolOn("BOOL") {
		t.Fatalf("envBoolOn: expected false")
	}

	t.Setenv("N", "100")
	if got := envInt64Bounded("N", 5, 1, 10); got != 5 {
		t.Fatalf("envInt64Bounded: expected fallback, got %d", got)
	}
	t.Setenv("N", "7")
	if got := envInt64Bounded("N", 5, 1, 10); got != 7 {
		t.Fatalf("envInt64Bounded: expected 7, got %d", got)
	}

	t.Setenv("P", "-1")
	if got := envInt64Positive("P", 9); got != 9 {
		t.Fatalf("envInt64Positive: expected fallback, got %d", got)
	}
	t.Setenv("P", "2")
	if got := envInt64Positive("P", 9); got != 2 {
		t.Fatalf("envInt64Positive: expected 2, got %d", got)
	}

	t.Setenv("U16", "999")
	if got := envUint16Max("U16", 3, 500); got != 3 {
		t.Fatalf("envUint16Max: expected fallback, got %d", got)
	}

	if got := parseCSV(" a, ,b "); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("parseCSV unexpected: %#v", got)
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("STAGE", "")
	t.Setenv("WEBAUTHN_ORIGINS", " https://a ,https://b ")
	t.Setenv("ATTESTATION_PUBLIC_KEY_IDS", "k1,k2")
	t.Setenv("TIP_ENABLED", "true")
	t.Setenv("TIP_DEFAULT_HOST_FEE_BPS", "250")
	t.Setenv("ENS_GATEWAY_ENABLED", "")
	t.Setenv("ENS_GATEWAY_CHAIN_ID", "")
	t.Setenv("ENS_GATEWAY_CHAIN_NAME", "")
	t.Setenv("ENS_GATEWAY_ROOT_NAME", "")

	cfg := Load()

	if cfg.Stage != "lab" {
		t.Fatalf("expected default stage lab, got %q", cfg.Stage)
	}
	if cfg.AppName != "lesser-host" {
		t.Fatalf("expected app name lesser-host, got %q", cfg.AppName)
	}
	if len(cfg.WebAuthnOrigins) != 2 {
		t.Fatalf("expected origins parsed, got %#v", cfg.WebAuthnOrigins)
	}
	if len(cfg.AttestationPublicKeyIDs) != 2 {
		t.Fatalf("expected public key ids parsed, got %#v", cfg.AttestationPublicKeyIDs)
	}
	if !cfg.TipEnabled {
		t.Fatalf("expected tip enabled")
	}
	if cfg.TipDefaultHostFeeBps != 250 {
		t.Fatalf("expected tip fee bps 250, got %d", cfg.TipDefaultHostFeeBps)
	}
	if cfg.ManagedParentDomain == "" || cfg.ManagedInstanceRoleName == "" || cfg.ManagedDefaultRegion == "" {
		t.Fatalf("expected managed defaults set")
	}
	if cfg.ManagedLesserBodyGitHubOwner == "" || cfg.ManagedLesserBodyGitHubRepo == "" {
		t.Fatalf("expected managed lesser-body defaults set")
	}
	if cfg.PaymentsCentsPer1000Credits <= 0 {
		t.Fatalf("expected payments pricing default set")
	}
	if cfg.SoulV2StrictIntegrity {
		t.Fatalf("expected strict integrity default off")
	}
	if cfg.SoulCommMailboxRetentionDays != 90 {
		t.Fatalf("expected mailbox retention default 90, got %d", cfg.SoulCommMailboxRetentionDays)
	}
}

func TestLoad_HostedGenesisMicroVMDefaultRuntimeEnvelope(t *testing.T) {
	cfg := Load()

	if got := cfg.HostedGenesisMicroVM.MaximumDurationSeconds; got != HostedGenesisMicroVMDefaultMaximumDurationSeconds {
		t.Fatalf("expected hosted genesis microvm default max duration %d, got %d", HostedGenesisMicroVMDefaultMaximumDurationSeconds, got)
	}
}

func TestLoad_HostedGenesisMicroVMLifetimePolicy(t *testing.T) {
	t.Setenv("HOSTED_GENESIS_MICROVM_ENABLED", "true")
	t.Setenv("APPTHEORY_MICROVM_CONTROLLER_ENDPOINT", " "+testMicroVMControllerEndpoint+" ")
	t.Setenv("HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SSM_PARAM", " "+testMicroVMAuthTokenParam+" ")
	t.Setenv("APPTHEORY_MICROVM_IMAGE_REF", " "+testMicroVMImageRef+" ")
	t.Setenv("APPTHEORY_MICROVM_IMAGE_VERSION", "29")
	t.Setenv("HOSTED_GENESIS_MICROVM_EXECUTION_ROLE_ARN", "arn:aws:iam::123456789012:role/hosted-genesis-test")
	t.Setenv("HOSTED_GENESIS_MICROVM_RUNTIME_LOG_GROUP", "/aws/lambda/microvms/hosted-genesis-test")
	t.Setenv("APPTHEORY_MICROVM_EGRESS_NETWORK_CONNECTOR_REFS", " "+testMicroVMEgressOne+", "+testMicroVMEgressTwo+" ")
	t.Setenv("APPTHEORY_MICROVM_INGRESS_NETWORK_CONNECTOR_REFS", " "+testMicroVMIngressOne+" ")
	t.Setenv("HOSTED_GENESIS_MICROVM_MAXIMUM_DURATION_SECONDS", "450")

	cfg := Load()

	if !cfg.HostedGenesisMicroVM.Complete() {
		t.Fatalf("expected complete hosted genesis microvm config: %#v", cfg.HostedGenesisMicroVM)
	}
	if cfg.HostedGenesisMicroVM.ControllerEndpoint != testMicroVMControllerEndpoint {
		t.Fatalf("unexpected controller endpoint: %q", cfg.HostedGenesisMicroVM.ControllerEndpoint)
	}
	if cfg.HostedGenesisMicroVM.NetworkConnectorRef != testMicroVMEgressOne {
		t.Fatalf("expected first egress connector as network connector ref, got %q", cfg.HostedGenesisMicroVM.NetworkConnectorRef)
	}
	if cfg.HostedGenesisMicroVM.MaximumDurationSeconds != 450 {
		t.Fatalf("expected maximum duration 450, got %d", cfg.HostedGenesisMicroVM.MaximumDurationSeconds)
	}
}

func TestLoad_HostedGenesisMicroVMCompactConfig(t *testing.T) {
	t.Setenv(HostedGenesisMicroVMConfigJSONEnv, `{
		"v": 2,
		"ep": " `+testMicroVMControllerEndpoint+` ",
		"ap": " `+testMicroVMAuthTokenParam+` ",
		"img": " `+testMicroVMImageRef+` ",
		"iv": "29",
		"er": "arn:aws:iam::123456789012:role/hosted-genesis-test",
		"lg": "/aws/lambda/microvms/hosted-genesis-test",
		"in": " `+testMicroVMIngressOne+`, `+testMicroVMIngressTwo+` ",
		"eg": " `+testMicroVMEgressOne+`, `+testMicroVMEgressTwo+` ",
		"max": 450
	}`)
	// Legacy per-variable env must not be required when the compact CDK-owned
	// config is present; the compact value is the deployment env-budget path.
	t.Setenv("HOSTED_GENESIS_MICROVM_ENABLED", "")
	t.Setenv("APPTHEORY_MICROVM_CONTROLLER_ENDPOINT", "")
	t.Setenv("HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SSM_PARAM", "")
	t.Setenv("APPTHEORY_MICROVM_IMAGE_REF", "")
	t.Setenv("APPTHEORY_MICROVM_EGRESS_NETWORK_CONNECTOR_REFS", "")
	t.Setenv("APPTHEORY_MICROVM_INGRESS_NETWORK_CONNECTOR_REFS", "")

	cfg := Load()

	assertTrue(t, "compact hosted genesis microvm config complete", cfg.HostedGenesisMicroVM.Complete())
	assertEqual(t, "compact controller endpoint", cfg.HostedGenesisMicroVM.ControllerEndpoint, testMicroVMControllerEndpoint)
	assertEqual(t, "compact auth-token param", cfg.HostedGenesisMicroVM.AuthTokenSSMParam, testMicroVMAuthTokenParam)
	assertEqual(t, "compact image ref", cfg.HostedGenesisMicroVM.ImageRef, testMicroVMImageRef)
	assertEqual(t, "compact image version", cfg.HostedGenesisMicroVM.ImageVersion, "29")
	assertEqual(t, "compact execution role", cfg.HostedGenesisMicroVM.ExecutionRoleARN, "arn:aws:iam::123456789012:role/hosted-genesis-test")
	assertEqual(t, "compact runtime log group", cfg.HostedGenesisMicroVM.RuntimeLogGroup, "/aws/lambda/microvms/hosted-genesis-test")
	assertEqual(t, "compact network connector ref", cfg.HostedGenesisMicroVM.NetworkConnectorRef, testMicroVMEgressOne)
	assertStringSliceEqual(t, "compact ingress refs", cfg.HostedGenesisMicroVM.IngressConnectorRefs, []string{testMicroVMIngressOne, testMicroVMIngressTwo})
	assertStringSliceEqual(t, "compact egress refs", cfg.HostedGenesisMicroVM.EgressConnectorRefs, []string{testMicroVMEgressOne, testMicroVMEgressTwo})
	assertEqual(t, "compact maximum duration", cfg.HostedGenesisMicroVM.MaximumDurationSeconds, int32(450))
}

func TestLoad_HostedGenesisMicroVMCompactConfigFailsClosed(t *testing.T) {
	t.Setenv(HostedGenesisMicroVMConfigJSONEnv, `{"v": 99, "ep": "https://controller.example/microvms"}`)
	t.Setenv("HOSTED_GENESIS_MICROVM_ENABLED", "true")
	t.Setenv("APPTHEORY_MICROVM_CONTROLLER_ENDPOINT", "https://legacy.example/microvms")
	t.Setenv("HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SSM_PARAM", "/legacy/token")
	t.Setenv("APPTHEORY_MICROVM_IMAGE_REF", "legacy-image")
	t.Setenv("APPTHEORY_MICROVM_EGRESS_NETWORK_CONNECTOR_REFS", "legacy-egress")

	cfg := Load()

	if !cfg.HostedGenesisMicroVM.Enabled {
		t.Fatalf("present invalid compact config should leave MicroVM enabled so startup fails closed loudly")
	}
	if cfg.HostedGenesisMicroVM.Complete() {
		t.Fatalf("invalid compact config must not fall back to legacy env: %#v", cfg.HostedGenesisMicroVM)
	}
	if cfg.HostedGenesisMicroVM.ControllerEndpoint != "" ||
		cfg.HostedGenesisMicroVM.AuthTokenSSMParam != "" ||
		cfg.HostedGenesisMicroVM.ImageRef != "" {
		t.Fatalf("invalid compact config leaked legacy values: %#v", cfg.HostedGenesisMicroVM)
	}
}

func TestLoad_ENSGatewayDefaultStageConfig(t *testing.T) {
	t.Setenv("STAGE", "")
	t.Setenv("ENS_GATEWAY_CHAIN_ID", "")
	t.Setenv("ENS_GATEWAY_CHAIN_NAME", "")
	t.Setenv("ENS_GATEWAY_ROOT_NAME", "")

	cfg := Load()

	if cfg.ENSGatewayChainID != 11155111 || cfg.ENSGatewayChainName != "sepolia" {
		t.Fatalf("expected lab ENS gateway default Sepolia, got chain_id=%d chain_name=%q", cfg.ENSGatewayChainID, cfg.ENSGatewayChainName)
	}
	if cfg.ENSGatewayRootName != "lessersoul.eth" {
		t.Fatalf("expected default ENS root, got %q", cfg.ENSGatewayRootName)
	}
}

func TestLoad_ENSGatewayConfigIndependentFromSoulRegistry(t *testing.T) {
	t.Setenv("STAGE", "live")
	t.Setenv("ENS_GATEWAY_ENABLED", "true")
	t.Setenv("ENS_GATEWAY_ROOT_NAME", " Lessersoul.ETH ")
	t.Setenv("ENS_GATEWAY_RESOLVER_ADDRESS", " 0x0000000000000000000000000000000000000001 ")
	t.Setenv("SOUL_ENABLED", "false")
	t.Setenv("SOUL_CHAIN_ID", "11155111")
	t.Setenv("SOUL_REGISTRY_CONTRACT_ADDRESS", "0x0000000000000000000000000000000000000002")

	cfg := Load()

	if !cfg.ENSGatewayEnabled {
		t.Fatalf("expected ENS gateway enabled from ENS-specific env")
	}
	if cfg.ENSGatewayChainID != 1 {
		t.Fatalf("expected live ENS gateway chain to stay mainnet, got %d", cfg.ENSGatewayChainID)
	}
	if cfg.ENSGatewayChainName != "mainnet" {
		t.Fatalf("expected live ENS gateway chain name mainnet, got %q", cfg.ENSGatewayChainName)
	}
	if cfg.ENSGatewayRootName != "lessersoul.eth" {
		t.Fatalf("expected normalized ENS root name, got %q", cfg.ENSGatewayRootName)
	}
	if cfg.ENSGatewayResolverAddress != "0x0000000000000000000000000000000000000001" {
		t.Fatalf("unexpected ENS resolver address: %q", cfg.ENSGatewayResolverAddress)
	}
	if cfg.SoulEnabled {
		t.Fatalf("expected legacy SoulRegistry config to remain disabled")
	}
	if cfg.SoulChainID != 11155111 {
		t.Fatalf("expected legacy SoulRegistry chain to remain separate Sepolia value, got %d", cfg.SoulChainID)
	}
}

func TestLoad_ENSGatewayEnabledLegacyFallback(t *testing.T) {
	t.Setenv("ENS_GATEWAY_ENABLED", "")
	t.Setenv("SOUL_ENABLED", "true")

	cfg := Load()

	if !cfg.ENSGatewayEnabled {
		t.Fatalf("expected legacy SOUL_ENABLED fallback while ENS_GATEWAY_ENABLED is unset")
	}
}

func TestLoad_SoulPublicOnChainAvatarEnabled(t *testing.T) {
	t.Setenv("SOUL_PUBLIC_ONCHAIN_AVATAR_ENABLED", "true")
	cfg := Load()
	if !cfg.SoulPublicOnChainAvatarEnabled {
		t.Fatalf("expected public on-chain avatar enrichment enabled")
	}
}

func TestLoad_SoulV2StrictIntegrity(t *testing.T) {
	t.Setenv("SOUL_V2_STRICT_INTEGRITY", "true")
	cfg := Load()
	if !cfg.SoulV2StrictIntegrity {
		t.Fatalf("expected strict integrity enabled")
	}
}

func TestLoad_SoulCommMailboxConfig(t *testing.T) {
	t.Setenv("SOUL_COMM_MAILBOX_BUCKET_NAME", " mailbox-bucket ")
	t.Setenv("SOUL_COMM_MAILBOX_RETENTION_DAYS", "120")
	cfg := Load()
	if cfg.SoulCommMailboxBucketName != "mailbox-bucket" {
		t.Fatalf("unexpected mailbox bucket: %q", cfg.SoulCommMailboxBucketName)
	}
	if cfg.SoulCommMailboxRetentionDays != 120 {
		t.Fatalf("unexpected mailbox retention: %d", cfg.SoulCommMailboxRetentionDays)
	}

	t.Setenv("SOUL_COMM_MAILBOX_RETENTION_DAYS", "999")
	cfg = Load()
	if cfg.SoulCommMailboxRetentionDays != 90 {
		t.Fatalf("expected invalid retention fallback, got %d", cfg.SoulCommMailboxRetentionDays)
	}
}
