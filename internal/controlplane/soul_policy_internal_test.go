package controlplane

import (
	"testing"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestEffectiveSoulAgentPolicyDoesNotGateCapabilitiesOnAnchorState(t *testing.T) {
	t.Parallel()

	for _, anchorState := range []string{
		models.SoulAnchorStateHostedOffchain,
		models.SoulAnchorStateImmutableOnchain,
	} {
		t.Run(anchorState, func(t *testing.T) {
			t.Parallel()

			identity := &models.SoulAgentIdentity{
				AgentID:                soulLifecycleTestAgentIDHex,
				Status:                 models.SoulAgentStatusActive,
				PolicyVersion:          models.SoulPolicyVersionHostedBoundSoulV1,
				AnchorState:            anchorState,
				EmailDefaultAllowed:    true,
				PhoneEntitlementStatus: models.SoulPhoneEntitlementProvisioned,
				SMSAllowed:             true,
				VoiceAllowed:           true,
				PublicPaidCallerAccess: models.SoulPublicPaidCallerAccessGrantable,
			}

			policy := effectiveSoulAgentPolicy(identity)
			if policy.AnchorState != anchorState {
				t.Fatalf("expected anchor state %q, got %#v", anchorState, policy)
			}
			if !policy.Capabilities.Email.DefaultAllowed ||
				!policy.Capabilities.Phone.SMSAllowed ||
				!policy.Capabilities.Phone.VoiceAllowed ||
				policy.CallerAccessPayment.PublicPaidCaller.Access != models.SoulPublicPaidCallerAccessGrantable {
				t.Fatalf("anchor assurance changed capability/access policy: %#v", policy)
			}

			for _, channel := range []string{commChannelEmail, commChannelSMS, commChannelVoice} {
				metrics := newSoulCommSendMetrics("lab", "inst1")
				if err := enforceSoulCommCapabilityPolicy(identity, validatedSoulCommSendRequest{channel: channel}, metrics); err != nil {
					t.Fatalf("anchor state %q should not block %s capability: %v", anchorState, channel, err)
				}
			}
		})
	}
}
