package store

import (
	"context"
	"fmt"
	"strings"

	runtimemicrovm "github.com/theory-cloud/apptheory/v3/runtime/microvm"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// HostedGenesisMicroVMRegistry adapts Host-owned operational cache rows to the
// AppTheory MicroVM SessionRegistry interface. It does not persist AppTheory's
// generic TableTheory registry model; Host derives its own tenant-scoped keys
// inside the repository/model boundary.
type HostedGenesisMicroVMRegistry struct {
	store *Store
}

var _ runtimemicrovm.SessionRegistry = (*HostedGenesisMicroVMRegistry)(nil)
var _ runtimemicrovm.SessionRegistryLister = (*HostedGenesisMicroVMRegistry)(nil)

// NewHostedGenesisMicroVMRegistry returns the Host-backed registry adapter used
// by the deployed hosted-genesis MicroVM controller.
func NewHostedGenesisMicroVMRegistry(store *Store) (*HostedGenesisMicroVMRegistry, error) {
	if store == nil || store.DB == nil {
		return nil, fmt.Errorf("hosted genesis microvm registry requires store")
	}
	return &HostedGenesisMicroVMRegistry{store: store}, nil
}

// Put validates and stores a safe AppTheory session record through Host's
// operational-cache model.
func (r *HostedGenesisMicroVMRegistry) Put(ctx context.Context, record runtimemicrovm.SessionRecord) (runtimemicrovm.SessionRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil || r.store == nil {
		return runtimemicrovm.SessionRecord{}, fmt.Errorf("hosted genesis microvm registry is unavailable")
	}
	item, err := models.NewHostedGenesisMicroVMExecutionFromSessionRecord(record)
	if err != nil {
		return runtimemicrovm.SessionRecord{}, err
	}
	if err := r.store.PutHostedGenesisMicroVMExecution(ctx, item); err != nil {
		return runtimemicrovm.SessionRecord{}, err
	}
	return item.ToSessionRecord()
}

// Get loads one tenant-bound operational cache row. Missing cache is returned
// as an error so the AppTheory reconstructing wrapper can rebuild from Host's
// HostedGenesisSession truth.
func (r *HostedGenesisMicroVMRegistry) Get(ctx context.Context, key runtimemicrovm.SessionKey) (runtimemicrovm.SessionRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil || r.store == nil {
		return runtimemicrovm.SessionRecord{}, fmt.Errorf("hosted genesis microvm registry is unavailable")
	}
	slug, err := hostedGenesisMicroVMRegistrySlugFromKey(key)
	if err != nil {
		return runtimemicrovm.SessionRecord{}, err
	}
	item, err := r.store.GetHostedGenesisMicroVMExecution(ctx, slug, key.Namespace, key.SessionID)
	if err != nil {
		return runtimemicrovm.SessionRecord{}, err
	}
	return item.ToSessionRecord()
}

// Delete removes only Host's operational cache row. The durable
// HostedGenesisSession remains source truth and can reconstruct future cache.
func (r *HostedGenesisMicroVMRegistry) Delete(ctx context.Context, key runtimemicrovm.SessionKey) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil || r.store == nil {
		return fmt.Errorf("hosted genesis microvm registry is unavailable")
	}
	slug, err := hostedGenesisMicroVMRegistrySlugFromKey(key)
	if err != nil {
		return err
	}
	return r.store.DeleteHostedGenesisMicroVMExecution(ctx, slug, key.Namespace, key.SessionID)
}

// List returns tenant-bound operational cache rows for controller list routes.
func (r *HostedGenesisMicroVMRegistry) List(ctx context.Context, input runtimemicrovm.SessionListInput) ([]runtimemicrovm.SessionRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("hosted genesis microvm registry is unavailable")
	}
	slug, err := hostedGenesisMicroVMRegistrySlugFromList(input)
	if err != nil {
		return nil, err
	}
	items, err := r.store.ListHostedGenesisMicroVMExecutions(ctx, slug, input.Namespace)
	if err != nil {
		return nil, err
	}
	out := make([]runtimemicrovm.SessionRecord, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		record, err := item.ToSessionRecord()
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, nil
}

func hostedGenesisMicroVMRegistrySlugFromKey(key runtimemicrovm.SessionKey) (string, error) {
	tenantID := strings.TrimSpace(key.TenantID)
	namespace := strings.TrimSpace(key.Namespace)
	sessionID := strings.TrimSpace(key.SessionID)
	slug := models.HostedGenesisMicroVMExecutionSlugFromTenantID(tenantID)
	if slug == "" || namespace == "" || sessionID == "" {
		return "", fmt.Errorf("hosted genesis microvm registry key is not tenant-bound")
	}
	return slug, nil
}

func hostedGenesisMicroVMRegistrySlugFromList(input runtimemicrovm.SessionListInput) (string, error) {
	tenantID := strings.TrimSpace(input.TenantID)
	namespace := strings.TrimSpace(input.Namespace)
	slug := models.HostedGenesisMicroVMExecutionSlugFromTenantID(tenantID)
	if slug == "" || namespace == "" {
		return "", fmt.Errorf("hosted genesis microvm registry list is not tenant-bound")
	}
	return slug, nil
}
