package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func tokenWithType(t *testing.T, s *Service, typ string, subject uuid.UUID) string {
	t.Helper()
	version := int32(1)
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		TenantID: uuid.New(), TokenVersion: &version, Typ: typ,
		RegisteredClaims: jwt.RegisteredClaims{Subject: subject.String(), IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))},
	})
	signed, err := tok.SignedString(s.secret)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestStaffParserRejectsCustomerToken(t *testing.T) {
	s, err := NewService(nil, []byte("test secret that is at least 32 bytes long"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.parse(tokenWithType(t, s, "customer", uuid.New())); err == nil {
		t.Fatal("staff parser accepted a customer token")
	}
}

func TestCustomerParserRejectsStaffToken(t *testing.T) {
	s, err := NewService(nil, []byte("test secret that is at least 32 bytes long"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.parseCustomer(tokenWithType(t, s, "", uuid.New())); err == nil {
		t.Fatal("customer parser accepted a staff token")
	}
}
