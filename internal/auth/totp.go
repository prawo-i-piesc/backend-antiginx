package auth

import (
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	TOTPIssuer = "AntiGinx"

	totpPeriod     = 30
	totpDigits     = otp.DigitsSix
	totpAlgorithm  = otp.AlgorithmSHA1
	totpSecretSize = 20
	totpSkew       = 1
)

type TOTPEnrollment struct {
	Secret     string
	OTPAuthURI string
}

func GenerateTOTPEnrollment(accountName string) (*TOTPEnrollment, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      TOTPIssuer,
		AccountName: accountName,
		Period:      totpPeriod,
		Digits:      totpDigits,
		Algorithm:   totpAlgorithm,
		SecretSize:  totpSecretSize,
	})
	if err != nil {
		return nil, err
	}

	return &TOTPEnrollment{
		Secret:     key.Secret(),
		OTPAuthURI: key.URL(),
	}, nil
}

func ValidateTOTPCode(secret, code string) bool {
	ok, err := totp.ValidateCustom(code, secret, time.Now().UTC(), totp.ValidateOpts{
		Period:    totpPeriod,
		Skew:      totpSkew,
		Digits:    totpDigits,
		Algorithm: totpAlgorithm,
	})
	return err == nil && ok
}
