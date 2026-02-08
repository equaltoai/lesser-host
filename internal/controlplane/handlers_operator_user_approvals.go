package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type portalUserApproval struct {
	Username       string    `json:"username"`
	Role           string    `json:"role"`
	Approved       bool      `json:"approved"`
	ApprovalStatus string    `json:"approval_status"`
	ReviewedBy     string    `json:"reviewed_by,omitempty"`
	ReviewedAt     time.Time `json:"reviewed_at,omitempty"`
	ApprovalNote   string    `json:"approval_note,omitempty"`
	DisplayName    string    `json:"display_name,omitempty"`
	Email          string    `json:"email,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	WalletAddress  string    `json:"wallet_address,omitempty"`
}

type listPortalUserApprovalsResponse struct {
	Users []portalUserApproval `json:"users"`
	Count int                  `json:"count"`
}

func portalUserApprovalFromModel(u models.User) portalUserApproval {
	return portalUserApproval{
		Username:       strings.TrimSpace(u.Username),
		Role:           strings.TrimSpace(u.Role),
		Approved:       u.Approved,
		ApprovalStatus: strings.TrimSpace(u.ApprovalStatus),
		ReviewedBy:     strings.TrimSpace(u.ReviewedBy),
		ReviewedAt:     u.ReviewedAt,
		ApprovalNote:   strings.TrimSpace(u.ApprovalNote),
		DisplayName:    strings.TrimSpace(u.DisplayName),
		Email:          strings.TrimSpace(u.Email),
		CreatedAt:      u.CreatedAt,
		WalletAddress:  walletAddressFromUsername(u.Username),
	}
}

func normalizeApprovalStatus(status string) (string, *apptheory.AppError) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		return models.UserApprovalStatusPending, nil
	}
	switch status {
	case models.UserApprovalStatusPending, models.UserApprovalStatusApproved, models.UserApprovalStatusRejected:
		return status, nil
	default:
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "invalid approval status"}
	}
}

func (s *Server) handleListPortalUserApprovals(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	statusValue := ""
	if ctx != nil && ctx.Request.Query != nil {
		if values, ok := ctx.Request.Query["status"]; ok && len(values) > 0 {
			statusValue = values[0]
		}
	}

	status, appErr := normalizeApprovalStatus(statusValue)
	if appErr != nil {
		return nil, appErr
	}

	items, err := listByGSI1PK[models.User](
		ctx,
		s,
		&models.User{},
		fmt.Sprintf("USER_APPROVAL#%s", status),
		200,
	)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list users"}
	}

	out := make([]portalUserApproval, 0, len(items))
	for _, user := range items {
		if strings.TrimSpace(user.Role) != models.RoleCustomer {
			continue
		}
		out = append(out, portalUserApprovalFromModel(user))
	}

	return apptheory.JSON(http.StatusOK, listPortalUserApprovalsResponse{Users: out, Count: len(out)})
}

func (s *Server) handleApprovePortalUser(ctx *apptheory.Context) (*apptheory.Response, error) {
	return s.handlePortalUserApprovalAction(ctx, models.UserApprovalStatusApproved)
}

func (s *Server) handleRejectPortalUser(ctx *apptheory.Context) (*apptheory.Response, error) {
	return s.handlePortalUserApprovalAction(ctx, models.UserApprovalStatusRejected)
}

func (s *Server) handlePortalUserApprovalAction(ctx *apptheory.Context, status string) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	username := strings.TrimSpace(ctx.Param("username"))
	if username == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "username is required"}
	}

	var user models.User
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.User{}).
		Where("PK", "=", fmt.Sprintf(models.KeyPatternUser, username)).
		Where("SK", "=", models.SKProfile).
		First(&user)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "user not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	if strings.TrimSpace(user.Role) != models.RoleCustomer {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "user is not a portal customer"}
	}

	currentStatus := strings.ToLower(strings.TrimSpace(user.ApprovalStatus))
	if currentStatus == "" && user.Approved {
		currentStatus = models.UserApprovalStatusApproved
	}
	if currentStatus == status && user.Approved == (status == models.UserApprovalStatusApproved) {
		return apptheory.JSON(http.StatusOK, portalUserApprovalFromModel(user))
	}

	note := parseOptionalReviewNote(ctx)
	now := time.Now().UTC()
	createdAt := user.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	gsi1PK := fmt.Sprintf("USER_APPROVAL#%s", status)
	gsi1SK := fmt.Sprintf("%s#%s", createdAt.UTC().Format(time.RFC3339Nano), username)
	actor := strings.TrimSpace(ctx.AuthIdentity)

	userKey := &models.User{
		PK: fmt.Sprintf(models.KeyPatternUser, username),
		SK: models.SKProfile,
	}

	audit := &models.AuditLogEntry{
		Actor:     actor,
		Action:    fmt.Sprintf("portal.user.%s", status),
		Target:    fmt.Sprintf("user:%s", username),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()

	if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.UpdateWithBuilder(userKey, func(ub core.UpdateBuilder) error {
			ub.Set("Approved", status == models.UserApprovalStatusApproved)
			ub.Set("ApprovalStatus", status)
			ub.Set("ReviewedBy", actor)
			ub.Set("ReviewedAt", now)
			ub.Set("ApprovalNote", strings.TrimSpace(note.Note))
			ub.Set("GSI1PK", gsi1PK)
			ub.Set("GSI1SK", gsi1SK)
			return nil
		}, tabletheory.IfExists())

		tx.Put(audit)
		return nil
	}); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return apptheory.JSON(http.StatusOK, portalUserApprovalFromModel(user))
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to update user"}
	}

	user.Approved = status == models.UserApprovalStatusApproved
	user.ApprovalStatus = status
	user.ReviewedBy = actor
	user.ReviewedAt = now
	user.ApprovalNote = strings.TrimSpace(note.Note)

	return apptheory.JSON(http.StatusOK, portalUserApprovalFromModel(user))
}
