package controlplane

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

var instanceSlugRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,61}[a-z0-9])?$`)

type createInstanceRequest struct {
	Slug  string `json:"slug"`
	Owner string `json:"owner,omitempty"`
}

type instanceResponse struct {
	Slug      string    `json:"slug"`
	Owner     string    `json:"owner,omitempty"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type listInstancesResponse struct {
	Instances []instanceResponse `json:"instances"`
	Count     int                `json:"count"`
}

type createInstanceKeyResponse struct {
	InstanceSlug string `json:"instance_slug"`
	Key          string `json:"key"`
	KeyID        string `json:"key_id"`
}

type setBudgetMonthRequest struct {
	IncludedCredits int64 `json:"included_credits"`
}

type budgetMonthResponse struct {
	InstanceSlug    string    `json:"instance_slug"`
	Month           string    `json:"month"`
	IncludedCredits int64     `json:"included_credits"`
	UsedCredits     int64     `json:"used_credits"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (s *Server) getInstance(ctx *apptheory.Context, slug string) (*models.Instance, error) {
	var inst models.Instance
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.Instance{}).
		Where("PK", "=", fmt.Sprintf("INSTANCE#%s", slug)).
		Where("SK", "=", models.SKMetadata).
		First(&inst)
	if err != nil {
		return nil, err
	}
	return &inst, nil
}

func (s *Server) handleCreateInstance(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}

	var req createInstanceRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(req.Slug))
	if slug == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "slug is required"}
	}
	if !instanceSlugRE.MatchString(slug) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid slug"}
	}

	now := time.Now().UTC()
	inst := &models.Instance{
		Slug:      slug,
		Owner:     strings.TrimSpace(req.Owner),
		Status:    models.InstanceStatusActive,
		CreatedAt: now,
	}
	if err := inst.UpdateKeys(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if err := s.store.DB.WithContext(ctx.Context()).Model(inst).IfNotExists().Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return nil, &apptheory.AppError{Code: "app.conflict", Message: "instance already exists"}
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create instance"}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "instance.create",
		Target:    fmt.Sprintf("instance:%s", slug),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(audit).Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to write audit log"}
	}

	return apptheory.JSON(http.StatusCreated, instanceResponse{
		Slug:      inst.Slug,
		Owner:     inst.Owner,
		Status:    inst.Status,
		CreatedAt: inst.CreatedAt,
	})
}

func (s *Server) handleListInstances(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}

	var items []*models.Instance
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.Instance{}).
		Filter("SK", "=", models.SKMetadata).
		Scan(&items)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list instances"}
	}

	out := make([]instanceResponse, 0, len(items))
	for _, inst := range items {
		out = append(out, instanceResponse{
			Slug:      inst.Slug,
			Owner:     inst.Owner,
			Status:    inst.Status,
			CreatedAt: inst.CreatedAt,
		})
	}

	return apptheory.JSON(http.StatusOK, listInstancesResponse{
		Instances: out,
		Count:     len(out),
	})
}

func (s *Server) handleCreateInstanceKey(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(ctx.Param("slug")))
	if slug == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "slug is required"}
	}

	if _, err := s.getInstance(ctx, slug); theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "instance not found"}
	} else if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	secret, err := newToken(32)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create key"}
	}
	plaintext := "lhk_" + secret

	sum := sha256.Sum256([]byte(plaintext))
	keyID := hex.EncodeToString(sum[:])

	now := time.Now().UTC()
	key := &models.InstanceKey{
		ID:           keyID,
		InstanceSlug: slug,
		CreatedAt:    now,
	}
	if err := key.UpdateKeys(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if err := s.store.DB.WithContext(ctx.Context()).Model(key).IfNotExists().Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create key"}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "instance_key.create",
		Target:    fmt.Sprintf("instance:%s", slug),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(audit).Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to write audit log"}
	}

	return apptheory.JSON(http.StatusCreated, createInstanceKeyResponse{
		InstanceSlug: slug,
		Key:          plaintext,
		KeyID:        keyID,
	})
}

func (s *Server) handleSetInstanceBudgetMonth(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(ctx.Param("slug")))
	if slug == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "slug is required"}
	}
	month := strings.TrimSpace(ctx.Param("month"))
	if month == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "month is required"}
	}
	if _, err := time.Parse("2006-01", month); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "month must be YYYY-MM"}
	}

	if _, err := s.getInstance(ctx, slug); theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "instance not found"}
	} else if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	var req setBudgetMonthRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}
	if req.IncludedCredits < 0 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "included_credits must be >= 0"}
	}

	// Preserve used credits if the record already exists.
	var existing models.InstanceBudgetMonth
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", fmt.Sprintf("INSTANCE#%s", slug)).
		Where("SK", "=", fmt.Sprintf("BUDGET#%s", month)).
		First(&existing)

	used := int64(0)
	if err == nil {
		used = existing.UsedCredits
	} else if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	now := time.Now().UTC()
	budget := &models.InstanceBudgetMonth{
		InstanceSlug:    slug,
		Month:           month,
		IncludedCredits: req.IncludedCredits,
		UsedCredits:     used,
		UpdatedAt:       now,
	}
	if err := budget.UpdateKeys(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if err := s.store.DB.WithContext(ctx.Context()).Model(budget).CreateOrUpdate(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to set budget"}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "budget.set",
		Target:    fmt.Sprintf("instance_budget:%s:%s", slug, month),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(audit).Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to write audit log"}
	}

	return apptheory.JSON(http.StatusOK, budgetMonthResponse{
		InstanceSlug:    slug,
		Month:           month,
		IncludedCredits: budget.IncludedCredits,
		UsedCredits:     budget.UsedCredits,
		UpdatedAt:       budget.UpdatedAt,
	})
}

