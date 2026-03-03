package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/domains"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type route53AssistResponse struct {
	OK           bool   `json:"ok"`
	HostedZoneID string `json:"hosted_zone_id,omitempty"`
	RecordName   string `json:"record_name"`
	RecordValue  string `json:"record_value"`
}

func normalizeRoute53ZoneName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.TrimSuffix(name, ".")
	return name
}

func domainInZone(domain, zone string) bool {
	domain = normalizeRoute53ZoneName(domain)
	zone = normalizeRoute53ZoneName(zone)
	if domain == "" || zone == "" {
		return false
	}
	return domain == zone || strings.HasSuffix(domain, "."+zone)
}

func ensureRoute53FQDN(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return name
	}
	if strings.HasSuffix(name, ".") {
		return name
	}
	return name + "."
}

func quoteTXTValue(value string) string {
	value = strings.TrimSpace(value)
	escaped := strings.ReplaceAll(value, `"`, `\"`)
	return `"` + escaped + `"`
}

func (s *Server) findRoute53HostedZoneID(ctx *apptheory.Context, domain string) (string, error) {
	if ctx == nil || s == nil || s.r53 == nil {
		return "", fmt.Errorf("route53 client is not initialized")
	}
	client, err := s.r53.get(ctx.Context())
	if err != nil {
		return "", err
	}

	bestID := ""
	bestLen := -1

	input := &route53.ListHostedZonesByNameInput{}
	for {
		out, err := client.ListHostedZonesByName(ctx.Context(), input)
		if err != nil {
			return "", err
		}

		bestID, bestLen = pickBestHostedZoneID(domain, out.HostedZones, bestID, bestLen)

		if !out.IsTruncated {
			break
		}

		input.DNSName = out.NextDNSName
		input.HostedZoneId = out.NextHostedZoneId
	}

	bestID = strings.TrimSpace(bestID)
	bestID = strings.TrimPrefix(bestID, "/hostedzone/")
	if bestID == "" {
		return "", fmt.Errorf("no matching Route53 hosted zone found for domain")
	}

	return bestID, nil
}

func pickBestHostedZoneID(domain string, hostedZones []r53types.HostedZone, bestID string, bestLen int) (string, int) {
	for _, hz := range hostedZones {
		name := normalizeRoute53ZoneName(aws.ToString(hz.Name))
		if !domainInZone(domain, name) {
			continue
		}
		if hz.Config != nil && hz.Config.PrivateZone {
			continue
		}
		if len(name) > bestLen {
			bestLen = len(name)
			bestID = aws.ToString(hz.Id)
		}
	}
	return bestID, bestLen
}

func (s *Server) handlePortalUpsertDomainVerificationRoute53(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(inst.Slug))

	domain, err := domains.NormalizeDomain(ctx.Param("domain"))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}

	var item models.Domain
	err = s.store.DB.WithContext(ctx.Context()).
		Model(&models.Domain{}).
		Where("PK", "=", fmt.Sprintf("DOMAIN#%s", domain)).
		Where("SK", "=", models.SKMetadata).
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "domain not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(item.InstanceSlug) != slug {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "domain not found"}
	}

	token := strings.TrimSpace(item.VerificationToken)
	if token == "" || strings.TrimSpace(item.Status) != models.DomainStatusPending {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "domain is not eligible for DNS assist"}
	}

	txtName := domainVerificationRecordPrefix + domain
	txtValue := domainVerificationValuePrefix + token

	zoneID := strings.TrimSpace(s.cfg.ManagedParentHostedZoneID)
	zoneID = strings.TrimPrefix(zoneID, "/hostedzone/")
	if zoneID == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "Route53 DNS assist is not configured"}
	}

	parentDomain := strings.TrimSpace(s.cfg.ManagedParentDomain)
	if parentDomain == "" {
		parentDomain = defaultManagedParentDomain
	}
	if !domainInZone(domain, parentDomain) {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "Route53 DNS assist is only available for managed domains"}
	}

	if s.r53 == nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "Route53 DNS assist is not configured"}
	}

	client, err := s.r53.get(ctx.Context())
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	_, err = client.ChangeResourceRecordSets(ctx.Context(), &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{
				{
					Action: r53types.ChangeActionUpsert,
					ResourceRecordSet: &r53types.ResourceRecordSet{
						Name: aws.String(ensureRoute53FQDN(txtName)),
						Type: r53types.RRTypeTxt,
						TTL:  aws.Int64(300),
						ResourceRecords: []r53types.ResourceRecord{
							{Value: aws.String(quoteTXTValue(txtValue))},
						},
					},
				},
			},
		},
	})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to update Route53 record"}
	}

	now := time.Now().UTC()
	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "portal.domain.dns_assist.route53",
		Target:    fmt.Sprintf("domain:%s", domain),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	})

	return apptheory.JSON(http.StatusOK, route53AssistResponse{
		OK:           true,
		HostedZoneID: zoneID,
		RecordName:   txtName,
		RecordValue:  txtValue,
	})
}
