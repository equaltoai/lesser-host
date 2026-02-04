package models

import (
	"fmt"
	"strings"
	"time"
)

const (
	InstanceStatusActive   = "active"
	InstanceStatusDisabled = "disabled"
)

type Instance struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	Slug                  string    `theorydb:"attr:slug" json:"slug"`
	Owner                 string    `theorydb:"attr:owner" json:"owner,omitempty"`
	Status                string    `theorydb:"attr:status" json:"status"`
	HostedPreviewsEnabled *bool     `theorydb:"attr:hostedPreviewsEnabled" json:"hosted_previews_enabled,omitempty"`
	RenderPolicy          string    `theorydb:"attr:renderPolicy" json:"render_policy,omitempty"` // always|suspicious
	CreatedAt             time.Time `theorydb:"attr:createdAt" json:"created_at"`
}

func (Instance) TableName() string { return MainTableName() }

func (i *Instance) BeforeCreate() error {
	if err := i.UpdateKeys(); err != nil {
		return err
	}
	if i.CreatedAt.IsZero() {
		i.CreatedAt = time.Now().UTC()
	}
	if strings.TrimSpace(i.Status) == "" {
		i.Status = InstanceStatusActive
	}
	if i.HostedPreviewsEnabled == nil {
		v := true
		i.HostedPreviewsEnabled = &v
	}
	if strings.TrimSpace(i.RenderPolicy) == "" {
		i.RenderPolicy = "suspicious"
	}
	return nil
}

func (i *Instance) UpdateKeys() error {
	slug := strings.TrimSpace(i.Slug)
	i.PK = fmt.Sprintf("INSTANCE#%s", slug)
	i.SK = SKMetadata
	return nil
}

func (i *Instance) GetPK() string { return i.PK }
func (i *Instance) GetSK() string { return i.SK }
