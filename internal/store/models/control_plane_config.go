package models

import "time"

// ControlPlaneConfig stores global control-plane configuration.
type ControlPlaneConfig struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	PrimaryAdminUsername string    `theorydb:"attr:primaryAdminUsername" json:"primary_admin_username"`
	BootstrappedAt       time.Time `theorydb:"attr:bootstrappedAt" json:"bootstrapped_at"`
}

// TableName returns the database table name for ControlPlaneConfig.
func (ControlPlaneConfig) TableName() string {
	return MainTableName()
}

// BeforeCreate sets keys before creating ControlPlaneConfig.
func (c *ControlPlaneConfig) BeforeCreate() error {
	c.PK = "CONTROL_PLANE"
	c.SK = "CONFIG"
	return nil
}

// UpdateKeys updates the database keys for ControlPlaneConfig.
func (c *ControlPlaneConfig) UpdateKeys() error {
	c.PK = "CONTROL_PLANE"
	c.SK = "CONFIG"
	return nil
}

// GetPK returns the partition key for ControlPlaneConfig.
func (c *ControlPlaneConfig) GetPK() string { return c.PK }

// GetSK returns the sort key for ControlPlaneConfig.
func (c *ControlPlaneConfig) GetSK() string { return c.SK }
