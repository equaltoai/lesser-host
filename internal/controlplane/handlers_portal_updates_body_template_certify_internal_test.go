package controlplane

import (
	"testing"

	"github.com/stretchr/testify/require"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestValidateCreateUpdateJobRequest_BodyTemplateCertifyRequiresOperator(t *testing.T) {
	t.Parallel()

	req := createUpdateJobRequest{
		BodyOnly:            true,
		LesserBodyVersion:   "v0.2.3",
		BodyTemplateCertify: true,
	}

	ctx := &apptheory.Context{AuthIdentity: "alice"}
	appErr := validateCreateUpdateJobRequest(ctx, req)
	require.NotNil(t, appErr)
	require.Equal(t, "app.forbidden", appErr.Code)

	operatorCtx := &apptheory.Context{AuthIdentity: "operator"}
	operatorCtx.Set(ctxKeyOperatorRole, models.RoleOperator)
	require.Nil(t, validateCreateUpdateJobRequest(operatorCtx, req))
}
