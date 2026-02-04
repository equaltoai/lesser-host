package models

import (
	"fmt"
	"strings"
	"time"
)

type OperatorSession struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	ID        string    `theorydb:"attr:id" json:"id"`
	Username  string    `theorydb:"attr:username" json:"username"`
	Role      string    `theorydb:"attr:role" json:"role"`
	Method    string    `theorydb:"attr:method" json:"method"`
	IssuedAt  time.Time `theorydb:"attr:issuedAt" json:"issued_at"`
	ExpiresAt time.Time `theorydb:"attr:expiresAt" json:"expires_at"`
}

func (OperatorSession) TableName() string {
	return MainTableName()
}

func (s *OperatorSession) UpdateKeys() error {
	s.ID = strings.TrimSpace(s.ID)
	s.PK = fmt.Sprintf(KeyPatternSession, s.ID)
	s.SK = "SESSION"
	s.TTL = s.ExpiresAt.Unix()
	return nil
}

func (s *OperatorSession) GetPK() string { return s.PK }
func (s *OperatorSession) GetSK() string { return s.SK }

