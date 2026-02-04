package models

import "time"

type ControlPlaneConfig struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	PrimaryAdminUsername string    `theorydb:"attr:primaryAdminUsername" json:"primary_admin_username"`
	BootstrappedAt       time.Time `theorydb:"attr:bootstrappedAt" json:"bootstrapped_at"`
}

func (ControlPlaneConfig) TableName() string {
	return MainTableName()
}

func (c *ControlPlaneConfig) BeforeCreate() error {
	c.PK = "CONTROL_PLANE"
	c.SK = "CONFIG"
	return nil
}

func (c *ControlPlaneConfig) UpdateKeys() error {
	c.PK = "CONTROL_PLANE"
	c.SK = "CONFIG"
	return nil
}

func (c *ControlPlaneConfig) GetPK() string { return c.PK }
func (c *ControlPlaneConfig) GetSK() string { return c.SK }
