package httpx

import (
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

type Code string

const (
	CodeValidationFailed Code = "VALIDATION_FAILED"
	CodeEmailTaken       Code = "EMAIL_TAKEN"

	CodeInvalidCredentials Code = "INVALID_CREDENTIALS"
	CodeAccountLocked      Code = "ACCOUNT_LOCKED"

	CodeMFAInvalidCode      Code = "MFA_INVALID_CODE"
	CodeMFATokenExpired     Code = "MFA_TOKEN_EXPIRED"
	CodeMFAAlreadyEnabled   Code = "MFA_ALREADY_ENABLED"
	CodeMFANotEnabled       Code = "MFA_NOT_ENABLED"
	CodeRecoveryCodeInvalid Code = "RECOVERY_CODE_INVALID"
	CodeStepUpRequired      Code = "STEP_UP_REQUIRED"

	CodeSessionExpired       Code = "SESSION_EXPIRED"
	CodeSessionReuseDetected Code = "SESSION_REUSE_DETECTED"

	CodeTokenInvalid Code = "TOKEN_INVALID"
	CodeTokenExpired Code = "TOKEN_EXPIRED"

	CodePasswordTooWeak   Code = "PASSWORD_TOO_WEAK"
	CodePasswordSameAsOld Code = "PASSWORD_SAME_AS_OLD"

	CodeOAuthStateInvalid     Code = "OAUTH_STATE_INVALID"
	CodeOAuthEmailUnverified  Code = "OAUTH_EMAIL_UNVERIFIED"
	CodeOAuthAccountConflict  Code = "OAUTH_ACCOUNT_CONFLICT"
	CodeOAuthProviderError    Code = "OAUTH_PROVIDER_ERROR"
	CodeProviderAlreadyLinked Code = "PROVIDER_ALREADY_LINKED"
	CodeLastLoginMethod       Code = "LAST_LOGIN_METHOD"

	CodeWebAuthnChallengeInvalid   Code = "WEBAUTHN_CHALLENGE_INVALID"
	CodeWebAuthnVerificationFailed Code = "WEBAUTHN_VERIFICATION_FAILED"
	CodeCredentialNotFound         Code = "CREDENTIAL_NOT_FOUND"
	CodePasskeyPasswordRequired    Code = "PASSKEY_PASSWORD_REQUIRED"

	CodeRateLimited Code = "RATE_LIMITED"
	CodeForbidden   Code = "FORBIDDEN"
	CodeInternal    Code = "INTERNAL"
)

type codeInfo struct {
	status  int
	message string
}

var codeTable = map[Code]codeInfo{
	CodeValidationFailed: {http.StatusBadRequest, "Validation failed"},
	CodeEmailTaken:       {http.StatusConflict, "User with this email already exists"},

	CodeInvalidCredentials: {http.StatusUnauthorized, "Invalid email or password"},
	CodeAccountLocked:      {http.StatusLocked, "Account temporarily locked after too many failed attempts"},

	CodeMFAInvalidCode:      {http.StatusUnauthorized, "Invalid verification code"},
	CodeMFATokenExpired:     {http.StatusUnauthorized, "Verification token expired or already used"},
	CodeMFAAlreadyEnabled:   {http.StatusConflict, "Two-factor authentication is already enabled"},
	CodeMFANotEnabled:       {http.StatusConflict, "Two-factor authentication is not enabled"},
	CodeRecoveryCodeInvalid: {http.StatusUnauthorized, "Invalid or already used recovery code"},
	CodeStepUpRequired:      {http.StatusForbidden, "This operation requires password confirmation"},

	CodeSessionExpired:       {http.StatusUnauthorized, "Session expired"},
	CodeSessionReuseDetected: {http.StatusUnauthorized, "Session reuse detected, all sessions revoked"},

	CodeTokenInvalid: {http.StatusBadRequest, "Invalid token"},
	CodeTokenExpired: {http.StatusBadRequest, "Token expired"},

	CodePasswordTooWeak:   {http.StatusBadRequest, "Password does not meet the password policy"},
	CodePasswordSameAsOld: {http.StatusBadRequest, "New password must differ from the current one"},

	CodeOAuthStateInvalid:     {http.StatusBadRequest, "Missing, invalid or expired OAuth state"},
	CodeOAuthEmailUnverified:  {http.StatusForbidden, "The provider did not verify this email address"},
	CodeOAuthAccountConflict:  {http.StatusConflict, "This email is already used by another account"},
	CodeOAuthProviderError:    {http.StatusBadGateway, "The identity provider returned an error"},
	CodeProviderAlreadyLinked: {http.StatusConflict, "This provider account is already linked elsewhere"},
	CodeLastLoginMethod:       {http.StatusConflict, "Cannot remove the last remaining login method"},

	CodeWebAuthnChallengeInvalid:   {http.StatusBadRequest, "Missing or expired WebAuthn challenge"},
	CodeWebAuthnVerificationFailed: {http.StatusUnauthorized, "WebAuthn verification failed"},
	CodeCredentialNotFound:         {http.StatusNotFound, "Credential not found"},
	CodePasskeyPasswordRequired:    {http.StatusForbidden, "This account signs in with a password first"},

	CodeRateLimited: {http.StatusTooManyRequests, "Too many requests"},
	CodeForbidden:   {http.StatusForbidden, "Forbidden"},
	CodeInternal:    {http.StatusInternalServerError, "Internal server error"},
}

type ErrorResponse struct {
	Error  string            `json:"error"`
	Code   Code              `json:"code"`
	Fields map[string]string `json:"fields,omitempty"`
}

func Status(code Code) int {
	if info, ok := codeTable[code]; ok {
		return info.status
	}
	return http.StatusInternalServerError
}

func Message(code Code) string {
	if info, ok := codeTable[code]; ok {
		return info.message
	}
	return codeTable[CodeInternal].message
}

func Fail(c *gin.Context, code Code) {
	c.AbortWithStatusJSON(Status(code), ErrorResponse{
		Error: Message(code),
		Code:  code,
	})
}

func FailWithFields(c *gin.Context, code Code, fields map[string]string) {
	c.AbortWithStatusJSON(Status(code), ErrorResponse{
		Error:  Message(code),
		Code:   code,
		Fields: fields,
	})
}

func FailRetryAfter(c *gin.Context, code Code, retryAfter time.Duration) {
	seconds := int(retryAfter.Round(time.Second).Seconds())
	if seconds < 1 {
		seconds = 1
	}
	c.Header("Retry-After", strconv.Itoa(seconds))
	Fail(c, code)
}

func FailValidation(c *gin.Context, err error) {
	FailWithFields(c, CodeValidationFailed, ValidationFields(err))
}

func ValidationFields(err error) map[string]string {
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return nil
	}

	fields := make(map[string]string, len(verrs))
	for _, fe := range verrs {
		fields[fe.Field()] = reasonOf(fe)
	}
	return fields
}

func reasonOf(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "required"
	case "email", "url", "uuid", "uuid4":
		return "invalid"
	}
	if param := fe.Param(); param != "" {
		return fe.Tag() + ":" + param
	}
	return fe.Tag()
}

func RegisterValidationFieldNames() {
	v, ok := binding.Validator.Engine().(*validator.Validate)
	if !ok {
		return
	}
	v.RegisterTagNameFunc(func(field reflect.StructField) string {
		name := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
		if name == "" || name == "-" {
			return field.Name
		}
		return name
	})
}
