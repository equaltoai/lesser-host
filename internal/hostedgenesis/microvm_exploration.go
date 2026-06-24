package hostedgenesis

import (
	"errors"
	"strings"

	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
)

const (
	// MicroVMNamespace is the AppTheory MicroVM namespace Host will use for
	// hosted-genesis execution sessions. The durable Host HostedGenesisSession
	// record remains the source of truth; this namespace scopes execution/cache
	// records only.
	MicroVMNamespace = "hosted-genesis"

	// MicroVMSourceOfTruth records the recovery invariant in MicroVM session
	// metadata without giving the MicroVM authoritative state ownership.
	MicroVMSourceOfTruth = "host-dynamodb-hosted-genesis-session"

	// MicroVMControllerID is the stable Host controller id used in AppTheory
	// session records for non-deploying contract tests and future controller code.
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

// NewMicroVMCreateRequest builds the AppTheory v1.14 MicroVM create envelope
// Host would use after a HostedGenesisSession row has been committed in
// DynamoDB. The function is a compile-safe exploration scaffold: it does not
// call AWS, create a MicroVM, enqueue SQS, or mutate Host state.
func NewMicroVMCreateRequest(
	requestID string,
	binding MicroVMSessionBinding,
	imageRef string,
	networkConnectorRef string,
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
		Command:             runtimemicrovm.CommandCreate,
		RequestID:           requestID,
		TenantID:            binding.TenantID(),
		Namespace:           MicroVMNamespace,
		AuthContext:         authContext(binding),
		SessionID:           strings.TrimSpace(binding.ConversationID),
		ImageRef:            imageRef,
		NetworkConnectorRef: networkConnectorRef,
		SessionSpec: runtimemicrovm.SessionSpec{
			Metadata: binding.Metadata(),
		},
	}, nil
}

// NewMicroVMCommandRequest builds a start/stop/status/session controller
// envelope for an existing hosted-genesis MicroVM execution session. Host state
// must decide whether this command is allowed before calling the controller.
func NewMicroVMCommandRequest(
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
	case runtimemicrovm.CommandStart,
		runtimemicrovm.CommandStop,
		runtimemicrovm.CommandStatus,
		runtimemicrovm.CommandSession:
		return runtimemicrovm.ControllerRequest{
			Command:     command,
			RequestID:   requestID,
			TenantID:    binding.TenantID(),
			Namespace:   MicroVMNamespace,
			AuthContext: authContext(binding),
			SessionID:   strings.TrimSpace(binding.ConversationID),
		}, nil
	default:
		return runtimemicrovm.ControllerRequest{}, errInvalidMicroVMBinding
	}
}

// ValidateAppTheoryMicroVMContracts validates the AppTheory v1.14 controller,
// registry, and escape-hatch contracts Host depends on for the next
// implementation milestone.
func ValidateAppTheoryMicroVMContracts() error {
	if err := runtimemicrovm.ValidateControllerContract(runtimemicrovm.DefaultControllerContract()); err != nil {
		return err
	}
	if err := runtimemicrovm.ValidateSessionRegistryContract(runtimemicrovm.DefaultSessionRegistryContract()); err != nil {
		return err
	}
	return runtimemicrovm.ValidateEscapeHatches(runtimemicrovm.EscapeHatches{})
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
