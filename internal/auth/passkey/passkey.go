package passkey

import (
	"errors"
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/models"
)

func New(rpID, rpName, origin string) (*webauthn.WebAuthn, error) {
	if strings.TrimSpace(rpID) == "" {
		return nil, errors.New("passkey: RPID is empty")
	}
	if strings.TrimSpace(origin) == "" {
		return nil, errors.New("passkey: origin is empty")
	}

	return webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: rpName,
		RPOrigins:     []string{origin},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationRequired,
		},
		AttestationPreference: protocol.PreferNoAttestation,
	})
}

type User struct {
	user        *models.User
	credentials []webauthn.Credential
}

func NewUser(user *models.User, stored []models.WebAuthnCredential) *User {
	credentials := make([]webauthn.Credential, 0, len(stored))
	for i := range stored {
		credentials = append(credentials, ToCredential(&stored[i]))
	}
	return &User{user: user, credentials: credentials}
}

func (u *User) WebAuthnID() []byte {
	id := u.user.ID
	return id[:]
}

func (u *User) WebAuthnName() string {
	return u.user.Email
}

func (u *User) WebAuthnDisplayName() string {
	if u.user.FullName != "" {
		return u.user.FullName
	}
	return u.user.Email
}

func (u *User) WebAuthnCredentials() []webauthn.Credential {
	return u.credentials
}

func (u *User) Model() *models.User {
	return u.user
}

func UserIDFromHandle(handle []byte) (uuid.UUID, error) {
	return uuid.FromBytes(handle)
}

func ToCredential(stored *models.WebAuthnCredential) webauthn.Credential {
	credential := webauthn.Credential{
		ID:              stored.CredentialID,
		PublicKey:       stored.PublicKey,
		AttestationType: stored.AttestationType,
		Flags: webauthn.CredentialFlags{
			BackupEligible: stored.BackupEligible,
			BackupState:    stored.BackupState,
		},
		Authenticator: webauthn.Authenticator{
			AAGUID:    stored.AAGUID,
			SignCount: stored.SignCount,
		},
	}

	for _, transport := range splitTransports(stored.Transports) {
		credential.Transport = append(credential.Transport, protocol.AuthenticatorTransport(transport))
	}
	return credential
}

func FromCredential(credential *webauthn.Credential) models.WebAuthnCredential {
	return models.WebAuthnCredential{
		CredentialID:    credential.ID,
		PublicKey:       credential.PublicKey,
		AttestationType: credential.AttestationType,
		Transports:      joinTransports(credential.Transport),
		AAGUID:          credential.Authenticator.AAGUID,
		SignCount:       credential.Authenticator.SignCount,
		BackupEligible:  credential.Flags.BackupEligible,
		BackupState:     credential.Flags.BackupState,
	}
}

func splitTransports(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	transports := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			transports = append(transports, trimmed)
		}
	}
	return transports
}

func joinTransports(transports []protocol.AuthenticatorTransport) string {
	if len(transports) == 0 {
		return ""
	}

	parts := make([]string, 0, len(transports))
	for _, transport := range transports {
		if trimmed := strings.TrimSpace(string(transport)); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, ",")
}
