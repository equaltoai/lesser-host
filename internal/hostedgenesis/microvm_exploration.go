package hostedgenesis

import (
	"errors"
	"strings"

	runtimemicrovm "github.com/theory-cloud/apptheory/v3/runtime/microvm"
)

const (
	// MicroVMNamespace is the AppTheory MicroVM namespace Host uses for
	// hosted-genesis execution sessions. The durable Host HostedGenesisSession
	// record remains the source of truth; this namespace scopes execution/cache
	// records only.
	MicroVMNamespace = "hosted-genesis"

	// MicroVMSourceOfTruth records the recovery invariant in MicroVM session
	// metadata without giving the MicroVM authoritative state ownership.
	MicroVMSourceOfTruth = "host-dynamodb-hosted-genesis-session"

	// MicroVMControllerID is the stable Host controller id used in AppTheory
	// session records for non-deploying contract tests and controller code.
	MicroVMControllerID = "lesser-host-hosted-genesis"

	// MicroVMAuthSubject is a sanitized service subject. It is not a bearer token,
	// Instance API key, wallet signature, or cloud credential.
	MicroVMAuthSubject = "lesser-host:hosted-genesis"
)

var errInvalidMicroVMBinding = errors.New("hosted genesis microvm binding is incomplete")

// MicroVMSessionBinding is the safe identifier envelope Host may pass to an
// AppTheory MicroVM controller. It intentionally carries only durable Host ids
// and tenant-bound routing ids; raw transcripts, prompts, bearer tokens,
// Instance API keys, AWS credentials, and provider secrets stay out of the
// MicroVM controller envelope.
type MicroVMSessionBinding struct {
	InstanceSlug   string
	RegistrationID string
	AgentID        string
	ConversationID string
	TurnID         string
}

// Validate fails closed if the binding cannot be tied back to one hosted
// genesis conversation in Host's DynamoDB state.
func (b MicroVMSessionBinding) Validate() error {
	if strings.TrimSpace(b.InstanceSlug) == "" ||
		strings.TrimSpace(b.RegistrationID) == "" ||
		strings.TrimSpace(b.AgentID) == "" ||
		strings.TrimSpace(b.ConversationID) == "" {
		return errInvalidMicroVMBinding
	}
	return nil
}

// TenantID returns the tenant boundary Host should use for the AppTheory
// controller. The slug is the managed-instance boundary; AppTheory also binds
// the namespace in its registry keys.
func (b MicroVMSessionBinding) TenantID() string {
	slug := strings.TrimSpace(b.InstanceSlug)
	if slug == "" {
		return ""
	}
	return "slug:" + slug
}

// Metadata returns the only HostedGenesisSession references that may cross into
// the AppTheory MicroVM session spec. These ids let Host reconcile execution
// state back to DynamoDB without making MicroVM memory/disk authoritative.
func (b MicroVMSessionBinding) Metadata() map[string]string {
	metadata := map[string]string{
		"source_of_truth": MicroVMSourceOfTruth,
		"registration_id": strings.TrimSpace(b.RegistrationID),
		"agent_id":        strings.TrimSpace(b.AgentID),
		"conversation_id": strings.TrimSpace(b.ConversationID),
	}
	if turnID := strings.TrimSpace(b.TurnID); turnID != "" {
		metadata["turn_id"] = turnID
	}
	return metadata
}

// NewMicroVMRunRequest builds the AppTheory M16 MicroVM run envelope Host
// uses after a HostedGenesisSession row has been committed in DynamoDB. The
// function is compile-safe and does not call AWS, enqueue SQS, or mutate Host
// state.
func NewMicroVMRunRequest(
	requestID string,
	binding MicroVMSessionBinding,
	imageRef string,
	networkConnectorRef string,
	ingressNetworkConnectorRefs []string,
	egressNetworkConnectorRefs []string,
) (runtimemicrovm.ControllerRequest, error) {
	requestID = strings.TrimSpace(requestID)
	imageRef = strings.TrimSpace(imageRef)
	networkConnectorRef = strings.TrimSpace(networkConnectorRef)
	if requestID == "" || imageRef == "" || networkConnectorRef == "" {
		return runtimemicrovm.ControllerRequest{}, errInvalidMicroVMBinding
	}
	if err := binding.Validate(); err != nil {
		return runtimemicrovm.ControllerRequest{}, err
	}
	return runtimemicrovm.ControllerRequest{
		Command:                     runtimemicrovm.CommandRun,
		RequestID:                   requestID,
		TenantID:                    binding.TenantID(),
		Namespace:                   MicroVMNamespace,
		AuthContext:                 authContext(binding),
		SessionID:                   strings.TrimSpace(binding.ConversationID),
		ImageRef:                    imageRef,
		NetworkConnectorRef:         networkConnectorRef,
		IngressNetworkConnectorRefs: normalizeStringSlice(ingressNetworkConnectorRefs),
		EgressNetworkConnectorRefs:  normalizeStringSlice(egressNetworkConnectorRefs),
		SessionSpec: runtimemicrovm.SessionSpec{
			Metadata: binding.Metadata(),
		},
	}, nil
}

// NewMicroVMOperationRequest builds a canonical M16 controller envelope for an
// existing hosted-genesis MicroVM execution session. Host state must decide
// whether this operation is allowed before calling the controller.
func NewMicroVMOperationRequest(
	command runtimemicrovm.Command,
	requestID string,
	binding MicroVMSessionBinding,
) (runtimemicrovm.ControllerRequest, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return runtimemicrovm.ControllerRequest{}, errInvalidMicroVMBinding
	}
	if err := binding.Validate(); err != nil {
		return runtimemicrovm.ControllerRequest{}, err
	}
	switch command {
	case runtimemicrovm.CommandGet,
		runtimemicrovm.CommandList,
		runtimemicrovm.CommandSuspend,
		runtimemicrovm.CommandResume,
		runtimemicrovm.CommandTerminate,
		runtimemicrovm.CommandAuthToken,
		runtimemicrovm.CommandShellAuthToken:
		req := runtimemicrovm.ControllerRequest{
			Command:     command,
			RequestID:   requestID,
			TenantID:    binding.TenantID(),
			Namespace:   MicroVMNamespace,
			AuthContext: authContext(binding),
		}
		if command != runtimemicrovm.CommandList {
			req.SessionID = strings.TrimSpace(binding.ConversationID)
		}
		if command == runtimemicrovm.CommandAuthToken {
			req.AllowedPortScope = []runtimemicrovm.ProviderPortScope{{Port: 443}}
		}
		return req, nil
	default:
		return runtimemicrovm.ControllerRequest{}, errInvalidMicroVMBinding
	}
}

// ValidateAppTheoryMicroVMContracts validates the AppTheory M16 operation
// contract and registry contract Host depends on for the active lab path.
func ValidateAppTheoryMicroVMContracts() error {
	if err := runtimemicrovm.ValidateOperationContract(runtimemicrovm.DefaultOperationContract()); err != nil {
		return err
	}
	if err := runtimemicrovm.ValidateRealLifecycleContract(runtimemicrovm.DefaultRealLifecycleContract()); err != nil {
		return err
	}
	return runtimemicrovm.ValidateSessionRegistryContract(runtimemicrovm.DefaultSessionRegistryContract())
}

func authContext(binding MicroVMSessionBinding) runtimemicrovm.AuthContext {
	return runtimemicrovm.AuthContext{
		Subject:   MicroVMAuthSubject,
		TenantID:  binding.TenantID(),
		Namespace: MicroVMNamespace,
		Entitlements: []string{
			"hosted_genesis:execute",
			"hosted_genesis:read_session",
		},
		Metadata: map[string]string{
			"instance_slug": strings.TrimSpace(binding.InstanceSlug),
		},
	}
}
