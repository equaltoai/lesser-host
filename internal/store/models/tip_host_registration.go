package models

import (
	"fmt"
	"strings"
	"time"
)

// TipHostRegistrationStatus* constants describe a host registration lifecycle.
const (
	TipHostRegistrationStatusPending   = "pending"
	TipHostRegistrationStatusVerified  = "verified"
	TipHostRegistrationStatusRejected  = "rejected"
	TipHostRegistrationStatusCompleted = "completed"
)

// TipHostRegistration represents an external host registry registration request with proof challenges.
type TipHostRegistration struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	GSI1PK string `theorydb:"index:gsi1,pk,attr:gsi1PK" json:"-"`
	GSI1SK string `theorydb:"index:gsi1,sk,attr:gsi1SK" json:"-"`

	ID string `theorydb:"attr:id" json:"id"`

	Kind string `theorydb:"attr:kind" json:"kind"`

	DomainRaw        string `theorydb:"attr:domainRaw" json:"domain_raw,omitempty"`
	DomainNormalized string `theorydb:"attr:domainNormalized" json:"domain_normalized"`
	HostIDHex        string `theorydb:"attr:hostIdHex" json:"host_id_hex"`

	ChainID     int64  `theorydb:"attr:chainID" json:"chain_id"`
	WalletType  string `theorydb:"attr:walletType" json:"wallet_type"`
	WalletAddr  string `theorydb:"attr:walletAddress" json:"wallet_address"`
	HostFeeBps  int64  `theorydb:"attr:hostFeeBps" json:"host_fee_bps"`
	TxMode      string `theorydb:"attr:txMode" json:"tx_mode,omitempty"` // safe|direct
	SafeAddress string `theorydb:"attr:safeAddress" json:"safe_address,omitempty"`

	WalletNonce   string `theorydb:"attr:walletNonce" json:"wallet_nonce,omitempty"`
	WalletMessage string `theorydb:"attr:walletMessage" json:"wallet_message,omitempty"`

	DNSToken  string `theorydb:"attr:dnsToken" json:"dns_token,omitempty"`
	HTTPToken string `theorydb:"attr:httpToken" json:"http_token,omitempty"`

	DNSVerified    bool      `theorydb:"attr:dnsVerified" json:"dns_verified,omitempty"`
	HTTPSVerified  bool      `theorydb:"attr:httpsVerified" json:"https_verified,omitempty"`
	WalletVerified bool      `theorydb:"attr:walletVerified" json:"wallet_verified,omitempty"`
	VerifiedAt     time.Time `theorydb:"attr:verifiedAt" json:"verified_at,omitempty"`

	Status string `theorydb:"attr:status" json:"status"`

	CreatedAt   time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt   time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
	ExpiresAt   time.Time `theorydb:"attr:expiresAt" json:"expires_at,omitempty"`
	CompletedAt time.Time `theorydb:"attr:completedAt" json:"completed_at,omitempty"`
}

// TableName returns the database table name for TipHostRegistration.
func (TipHostRegistration) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating TipHostRegistration.
func (r *TipHostRegistration) BeforeCreate() error {
	if err := r.UpdateKeys(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	if r.ExpiresAt.IsZero() {
		r.ExpiresAt = now.Add(30 * time.Minute)
	}
	r.TTL = r.ExpiresAt.Unix()
	if strings.TrimSpace(r.Status) == "" {
		r.Status = TipHostRegistrationStatusPending
	}
	if strings.TrimSpace(r.Kind) == "" {
		r.Kind = TipRegistryOperationKindRegisterHost
	}
	if strings.TrimSpace(r.WalletType) == "" {
		r.WalletType = walletTypeEthereum
	}
	r.updateGSI1()
	return nil
}

// BeforeUpdate updates timestamps and TTL before updating TipHostRegistration.
func (r *TipHostRegistration) BeforeUpdate() error {
	r.UpdatedAt = time.Now().UTC()
	if !r.ExpiresAt.IsZero() {
		r.TTL = r.ExpiresAt.Unix()
	}
	r.updateGSI1()
	return r.UpdateKeys()
}

// UpdateKeys updates the database keys for TipHostRegistration.
func (r *TipHostRegistration) UpdateKeys() error {
	r.ID = strings.TrimSpace(r.ID)
	r.Kind = strings.ToLower(strings.TrimSpace(r.Kind))
	r.DomainRaw = strings.TrimSpace(r.DomainRaw)
	r.DomainNormalized = strings.ToLower(strings.TrimSpace(r.DomainNormalized))
	r.HostIDHex = strings.ToLower(strings.TrimSpace(r.HostIDHex))
	r.WalletType = strings.TrimSpace(r.WalletType)
	r.WalletAddr = strings.ToLower(strings.TrimSpace(r.WalletAddr))
	r.TxMode = strings.ToLower(strings.TrimSpace(r.TxMode))
	r.SafeAddress = strings.ToLower(strings.TrimSpace(r.SafeAddress))
	r.WalletNonce = strings.TrimSpace(r.WalletNonce)
	r.WalletMessage = strings.TrimSpace(r.WalletMessage)
	r.DNSToken = strings.TrimSpace(r.DNSToken)
	r.HTTPToken = strings.TrimSpace(r.HTTPToken)
	r.Status = strings.ToLower(strings.TrimSpace(r.Status))

	r.PK = fmt.Sprintf("TIP_HOST_REG#%s", r.ID)
	r.SK = "REG"

	if !r.ExpiresAt.IsZero() {
		r.TTL = r.ExpiresAt.Unix()
	}

	return nil
}

// GetPK returns the partition key for TipHostRegistration.
func (r *TipHostRegistration) GetPK() string { return r.PK }

// GetSK returns the sort key for TipHostRegistration.
func (r *TipHostRegistration) GetSK() string { return r.SK }

func (r *TipHostRegistration) updateGSI1() {
	if r == nil {
		return
	}
	status := strings.ToLower(strings.TrimSpace(r.Status))
	if status == "" {
		r.GSI1PK = ""
		r.GSI1SK = ""
		return
	}

	createdAt := r.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	r.GSI1PK = fmt.Sprintf("TIP_HOST_REG_STATUS#%s", status)
	r.GSI1SK = fmt.Sprintf("%s#%s", createdAt.UTC().Format(time.RFC3339Nano), strings.TrimSpace(r.ID))
}
