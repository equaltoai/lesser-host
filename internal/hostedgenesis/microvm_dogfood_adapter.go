package hostedgenesis

import (
	"context"
	"time"

	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
)

// ProvisionalDogfoodMicroVMClient is Host's M3-only bounded adapter behind the
// AppTheory runtime/microvm.Client boundary. AppTheory v1.14.0 does not ship a
// concrete Go AWS Lambda MicroVM client adapter; Factory routed the gap to the
// AppTheory steward as delivery-bcb585616b891657. Until AppTheory provides that
// constrained adapter, this dogfood client delegates to AppTheory's registry
// client so controller/auth/envelope/cache contracts can be exercised without a
// raw AWS SDK escape hatch.
//
// This type must not grow public raw SDK accessors, bearer-token fields, raw
// lifecycle hook payloads, or Host business-state decisions. HostedGenesisSession
// remains Host's source truth; AppTheory registry state remains cache/lifecycle.
type ProvisionalDogfoodMicroVMClient struct {
	delegate runtimemicrovm.Client
}

var _ runtimemicrovm.Client = (*ProvisionalDogfoodMicroVMClient)(nil)

// NewProvisionalDogfoodMicroVMClient wraps an AppTheory SessionRegistry in the
// upstream RegistryClient and keeps Host code behind the microvm.Client surface.
func NewProvisionalDogfoodMicroVMClient(registry runtimemicrovm.SessionRegistry, ttl time.Duration) (*ProvisionalDogfoodMicroVMClient, error) {
	opts := []runtimemicrovm.RegistryClientOption{}
	if ttl > 0 {
		opts = append(opts, runtimemicrovm.WithRegistryClientTTL(ttl))
	}
	delegate, err := runtimemicrovm.NewRegistryClient(registry, opts...)
	if err != nil {
		return nil, err
	}
	return &ProvisionalDogfoodMicroVMClient{delegate: delegate}, nil
}

func (c *ProvisionalDogfoodMicroVMClient) Create(ctx context.Context, input runtimemicrovm.CreateSessionInput) (runtimemicrovm.SessionRecord, error) {
	return c.delegate.Create(ctx, input)
}

func (c *ProvisionalDogfoodMicroVMClient) Start(ctx context.Context, input runtimemicrovm.SessionCommandInput) (runtimemicrovm.SessionRecord, error) {
	return c.delegate.Start(ctx, input)
}

func (c *ProvisionalDogfoodMicroVMClient) Stop(ctx context.Context, input runtimemicrovm.SessionCommandInput) (runtimemicrovm.SessionRecord, error) {
	return c.delegate.Stop(ctx, input)
}

func (c *ProvisionalDogfoodMicroVMClient) Status(ctx context.Context, input runtimemicrovm.SessionQueryInput) (runtimemicrovm.SessionStatus, error) {
	return c.delegate.Status(ctx, input)
}

func (c *ProvisionalDogfoodMicroVMClient) Session(ctx context.Context, input runtimemicrovm.SessionQueryInput) (runtimemicrovm.SessionRecord, error) {
	return c.delegate.Session(ctx, input)
}
