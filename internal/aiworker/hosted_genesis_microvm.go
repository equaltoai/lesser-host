package aiworker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	hostedGenesisWorkerMicroVMDispatcherInitTimeout = 5 * time.Second
	hostedGenesisWorkerMicroVMHTTPTimeout           = 30 * time.Second
	hostedGenesisWorkerMicroVMRunTimeout            = 90 * time.Second
)

type hostedGenesisWorkerMicroVMDispatcherOptions struct {
	httpClient      *http.Client
	authToken       string
	ssmGetParameter func(ctx context.Context, name string) (string, error)
}

type hostedGenesisMicroVMTurnController interface {
	StartMicroVMRun(ctx context.Context, requestID string, binding hostedgenesis.MicroVMSessionBinding) (hostedgenesis.MicroVMDispatchResult, error)
	EnsureMicroVMTurnSession(ctx context.Context, requestID string, binding hostedgenesis.MicroVMSessionBinding, ref hostedgenesis.MicroVMLifecycleRef) (hostedgenesis.MicroVMDispatchResult, error)
	InvokeMicroVMTurn(ctx context.Context, requestID string, binding hostedgenesis.MicroVMSessionBinding) error
	WaitAndInvokeMicroVMTurn(ctx context.Context, requestID string, binding hostedgenesis.MicroVMSessionBinding) (hostedgenesis.MicroVMDispatchResult, error)
}

var hostedGenesisWorkerMicroVMDispatcherBuilder = func(ctx context.Context, cfg config.Config, ssmGetParameter func(ctx context.Context, name string) (string, error), opts hostedGenesisWorkerMicroVMDispatcherOptions) hostedgenesis.MicroVMDispatcher {
	return newHostedGenesisWorkerMicroVMDispatcher(ctx, cfg, ssmGetParameter, opts)
}

func defaultAIWorkerSSMGetParameter(ctx context.Context, name string) (string, error) {
	return secrets.GetSSMParameter(ctx, nil, name)
}

func newHostedGenesisWorkerMicroVMDispatcher(ctx context.Context, cfg config.Config, ssmGetParameter func(ctx context.Context, name string) (string, error), opts hostedGenesisWorkerMicroVMDispatcherOptions) hostedgenesis.MicroVMDispatcher {
	microvmCfg := cfg.HostedGenesisMicroVM
	if !microvmCfg.Complete() {
		if microvmCfg.Enabled {
			log.Printf("aiworker: hosted genesis microvm dispatcher disabled (incomplete config) endpoint_set=%t auth_token_ssm_param_set=%t image_ref_set=%t network_connector_set=%t",
				microvmCfg.ControllerEndpoint != "", microvmCfg.AuthTokenSSMParam != "", microvmCfg.ImageRef != "", microvmCfg.NetworkConnectorRef != "")
		}
		return nil
	}
	authToken := strings.TrimSpace(opts.authToken)
	if authToken == "" {
		getter := opts.ssmGetParameter
		if getter == nil {
			getter = ssmGetParameter
		}
		if getter == nil {
			log.Printf("aiworker: hosted genesis microvm dispatcher unavailable (auth token getter missing)")
			return nil
		}
		token, err := getter(ctx, microvmCfg.AuthTokenSSMParam)
		if err != nil {
			log.Printf("aiworker: hosted genesis microvm dispatcher unavailable (auth token fetch failed) err=%v", err)
			return nil
		}
		authToken = strings.TrimSpace(token)
	}
	if authToken == "" {
		log.Printf("aiworker: hosted genesis microvm dispatcher unavailable (auth token is empty)")
		return nil
	}
	httpClient := opts.httpClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: hostedGenesisWorkerMicroVMHTTPTimeout}
	}
	dispatcher, err := hostedgenesis.NewHTTPControllerDispatcher(hostedgenesis.HTTPControllerDispatcherConfig{
		Endpoint:             microvmCfg.ControllerEndpoint,
		AuthToken:            authToken,
		ImageRef:             microvmCfg.ImageRef,
		NetworkConnectorRef:  microvmCfg.NetworkConnectorRef,
		IngressConnectorRefs: append([]string(nil), microvmCfg.IngressConnectorRefs...),
		EgressConnectorRefs:  append([]string(nil), microvmCfg.EgressConnectorRefs...),
		MaxDurationSeconds:   microvmCfg.MaximumDurationSeconds,
		HTTPClient:           httpClient,
	})
	if err != nil {
		log.Printf("aiworker: hosted genesis microvm dispatcher unavailable (http dispatcher construction failed) err=%v", err)
		return nil
	}
	log.Printf("aiworker: hosted genesis microvm dispatcher wired stage=%s endpoint=%s image_ref=%s max_duration_seconds=%d",
		strings.TrimSpace(cfg.Stage), microvmCfg.ControllerEndpoint, microvmCfg.ImageRef, microvmCfg.MaximumDurationSeconds)
	return dispatcher
}

func (s *Server) processHostedGenesisMicroVMDispatch(ctx context.Context, workerRequestID string, msg hostedgenesis.QueueMessage) error {
	st, ok := s.hostedGenesisStore()
	if !ok {
		return fmt.Errorf("hosted genesis store not initialized")
	}
	_, conv, session, err := s.loadAndValidateHostedGenesisJob(ctx, st, msg)
	if err != nil {
		return err
	}
	if !hostedGenesisMicroVMDispatchJobReady(conv, session, msg) {
		return nil
	}
	if s.hostedGenesisMicroVMDispatcher == nil {
		log.Printf("aiworker: hosted genesis microvm dispatcher unavailable agent_hash=%s conversation_hash=%s", hostedGenesisAuditHash(msg.AgentID), hostedGenesisAuditHash(msg.ConversationID))
		return s.markHostedGenesisConversationFailed(ctx, st, conv, session, hostedGenesisFailureMicroVMUnavailable, firstNonEmptyWorker(msg.RequestID, workerRequestID))
	}
	binding := session.MicroVMSessionBinding()
	if err := binding.Validate(); err != nil {
		log.Printf("aiworker: hosted genesis microvm binding invalid agent_hash=%s conversation_hash=%s err=%v", hostedGenesisAuditHash(msg.AgentID), hostedGenesisAuditHash(msg.ConversationID), err)
		return s.markHostedGenesisConversationFailed(ctx, st, conv, session, hostedGenesisFailureMicroVMUnavailable, firstNonEmptyWorker(msg.RequestID, workerRequestID))
	}
	requestID := firstNonEmptyWorker(msg.RequestID, workerRequestID)
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), hostedGenesisWorkerMicroVMRunTimeout)
	defer cancel()

	controller, ok := s.hostedGenesisMicroVMDispatcher.(hostedGenesisMicroVMTurnController)
	if ok {
		// A HostedGenesisSession is durable conversation truth; an AppTheory
		// MicroVM session is bounded execution/cache state. Reuse a Host-recorded
		// lifecycle ref only after the controller revalidates it, resume suspended
		// sessions through AppTheory, and relaunch only when the controller proves
		// the ref is terminal/expired. This keeps checkpoint/relaunch/replay as the
		// correctness path; process memory remains an optional fast path after Host
		// status/version/lifecycle revalidation.
		dispatch, invokeWithWait, dispatchErr := ensureOrStartHostedGenesisMicroVMTurn(runCtx, controller, requestID, binding, session)
		if dispatchErr != nil {
			log.Printf("aiworker: hosted genesis microvm dispatch failed agent_hash=%s conversation_hash=%s err=%v", hostedGenesisAuditHash(msg.AgentID), hostedGenesisAuditHash(msg.ConversationID), dispatchErr)
			return s.markHostedGenesisConversationFailed(ctx, st, conv, session, hostedGenesisFailureMicroVMUnavailable, requestID)
		}
		progressed, persistErr := persistHostedGenesisMicroVMDispatchLifecycle(ctx, st, session, dispatch, requestID, time.Now().UTC())
		if persistErr != nil {
			log.Printf("aiworker: hosted genesis microvm lifecycle persist failed agent_hash=%s conversation_hash=%s err=%v", hostedGenesisAuditHash(msg.AgentID), hostedGenesisAuditHash(msg.ConversationID), persistErr)
			return persistErr
		}
		session = progressed
		if invokeWithWait {
			if _, invokeErr := controller.WaitAndInvokeMicroVMTurn(runCtx, requestID, binding); invokeErr != nil {
				log.Printf("aiworker: hosted genesis microvm invoke failed agent_hash=%s conversation_hash=%s err=%v", hostedGenesisAuditHash(msg.AgentID), hostedGenesisAuditHash(msg.ConversationID), invokeErr)
				return s.markHostedGenesisConversationFailed(ctx, st, conv, session, hostedGenesisFailureMicroVMUnavailable, requestID)
			}
			return nil
		}
		if invokeErr := controller.InvokeMicroVMTurn(runCtx, requestID, binding); invokeErr != nil {
			log.Printf("aiworker: hosted genesis microvm invoke failed agent_hash=%s conversation_hash=%s err=%v", hostedGenesisAuditHash(msg.AgentID), hostedGenesisAuditHash(msg.ConversationID), invokeErr)
			return s.markHostedGenesisConversationFailed(ctx, st, conv, session, hostedGenesisFailureMicroVMUnavailable, requestID)
		}
		return nil
	}

	dispatch, dispatchErr := s.hostedGenesisMicroVMDispatcher.DispatchMicroVMRun(runCtx, requestID, binding)
	if dispatchErr != nil {
		log.Printf("aiworker: hosted genesis microvm dispatch failed agent_hash=%s conversation_hash=%s err=%v", hostedGenesisAuditHash(msg.AgentID), hostedGenesisAuditHash(msg.ConversationID), dispatchErr)
		return s.markHostedGenesisConversationFailed(ctx, st, conv, session, hostedGenesisFailureMicroVMUnavailable, requestID)
	}
	_, persistErr := persistHostedGenesisMicroVMDispatchLifecycle(ctx, st, session, dispatch, requestID, time.Now().UTC())
	return persistErr
}

func ensureOrStartHostedGenesisMicroVMTurn(ctx context.Context, controller hostedGenesisMicroVMTurnController, requestID string, binding hostedgenesis.MicroVMSessionBinding, session *models.HostedGenesisSession) (hostedgenesis.MicroVMDispatchResult, bool, error) {
	if controller == nil {
		return hostedgenesis.MicroVMDispatchResult{}, false, hostedgenesis.ErrMicroVMDispatchUnavailable
	}
	if session != nil && session.MicroVMLifecycleRef != nil {
		ensured, err := controller.EnsureMicroVMTurnSession(ctx, requestID, binding, *session.MicroVMLifecycleRef)
		if err == nil {
			return ensured, false, nil
		}
		if !errors.Is(err, hostedgenesis.ErrMicroVMRelaunchRequired) {
			return hostedgenesis.MicroVMDispatchResult{}, false, err
		}
	}
	started, err := controller.StartMicroVMRun(ctx, requestID, binding)
	if err != nil {
		return hostedgenesis.MicroVMDispatchResult{}, false, err
	}
	return started, true, nil
}

func hostedGenesisMicroVMDispatchJobReady(conv *models.SoulAgentMintConversation, session *models.HostedGenesisSession, msg hostedgenesis.QueueMessage) bool {
	if conv == nil || session == nil {
		return false
	}
	if hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusInProgress {
		return false
	}
	return strings.TrimSpace(session.LatestTurnID) == strings.TrimSpace(msg.TurnID)
}

func persistHostedGenesisMicroVMDispatchLifecycle(ctx context.Context, st hostedGenesisStore, session *models.HostedGenesisSession, dispatch hostedgenesis.MicroVMDispatchResult, requestID string, now time.Time) (*models.HostedGenesisSession, error) {
	if st == nil || session == nil {
		return nil, fmt.Errorf("hosted genesis session store is not initialized")
	}
	progressed := cloneHostedGenesisSessionForWorker(session)
	if err := progressed.ApplyMicroVMLifecycleRef(dispatch.LifecycleRef); err != nil {
		return nil, err
	}
	progressed.Status = string(hostedgenesis.StatusInProgress)
	progressed.RequestID = strings.TrimSpace(requestID)
	progressed.UpdatedAt = now.UTC()
	progressed.CompletedAt = time.Time{}
	if err := st.UpdateHostedGenesisSession(ctx, progressed, session.Version, hostedgenesis.StatusInProgress); err != nil {
		return nil, err
	}
	progressed.Version = session.Version + 1
	return progressed, nil
}

func cloneHostedGenesisSessionForWorker(session *models.HostedGenesisSession) *models.HostedGenesisSession {
	if session == nil {
		return nil
	}
	copy := *session
	copy.TurnLedger = append([]hostedgenesis.TurnLedgerEntry(nil), session.TurnLedger...)
	if session.MicroVMLifecycleRef != nil {
		ref := *session.MicroVMLifecycleRef
		copy.MicroVMLifecycleRef = &ref
	}
	if session.DeclarationCheckpoint != nil {
		cp := *session.DeclarationCheckpoint
		copy.DeclarationCheckpoint = &cp
	}
	if session.Failure != nil {
		failure := *session.Failure
		copy.Failure = &failure
	}
	if session.TraceIDs != nil {
		trace := *session.TraceIDs
		copy.TraceIDs = &trace
	}
	if session.VMCheckpoint != nil {
		checkpoint := *session.VMCheckpoint
		copy.VMCheckpoint = &checkpoint
	}
	return &copy
}
