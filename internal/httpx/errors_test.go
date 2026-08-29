package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

var specStatuses = map[Code]int{
	CodeValidationFailed:           http.StatusBadRequest,
	CodeEmailTaken:                 http.StatusConflict,
	CodeInvalidCredentials:         http.StatusUnauthorized,
	CodeAccountLocked:              http.StatusLocked,
	CodeMFAInvalidCode:             http.StatusUnauthorized,
	CodeMFATokenExpired:            http.StatusUnauthorized,
	CodeMFAAlreadyEnabled:          http.StatusConflict,
	CodeMFANotEnabled:              http.StatusConflict,
	CodeRecoveryCodeInvalid:        http.StatusUnauthorized,
	CodeStepUpRequired:             http.StatusForbidden,
	CodeSessionExpired:             http.StatusUnauthorized,
	CodeSessionReuseDetected:       http.StatusUnauthorized,
	CodeTokenInvalid:               http.StatusBadRequest,
	CodeTokenExpired:               http.StatusBadRequest,
	CodePasswordTooWeak:            http.StatusBadRequest,
	CodePasswordSameAsOld:          http.StatusBadRequest,
	CodeOAuthStateInvalid:          http.StatusBadRequest,
	CodeOAuthEmailUnverified:       http.StatusForbidden,
	CodeOAuthAccountConflict:       http.StatusConflict,
	CodeOAuthProviderError:         http.StatusBadGateway,
	CodeProviderAlreadyLinked:      http.StatusConflict,
	CodeLastLoginMethod:            http.StatusConflict,
	CodeWebAuthnChallengeInvalid:   http.StatusBadRequest,
	CodeWebAuthnVerificationFailed: http.StatusUnauthorized,
	CodeCredentialNotFound:         http.StatusNotFound,
	CodeRateLimited:                http.StatusTooManyRequests,
	CodeForbidden:                  http.StatusForbidden,
	CodeInternal:                   http.StatusInternalServerError,
}

func TestCodeTableMatchesSpecification(t *testing.T) {
	if len(codeTable) != len(specStatuses) {
		t.Errorf("codeTable has %d entries, specification lists %d", len(codeTable), len(specStatuses))
	}

	for code, want := range specStatuses {
		info, ok := codeTable[code]
		if !ok {
			t.Errorf("code %s is missing from codeTable", code)
			continue
		}
		if info.status != want {
			t.Errorf("code %s has status %d, want %d", code, info.status, want)
		}
		if strings.TrimSpace(info.message) == "" {
			t.Errorf("code %s has an empty message", code)
		}
	}

	for code := range codeTable {
		if _, ok := specStatuses[code]; !ok {
			t.Errorf("codeTable has %s, which the specification does not list", code)
		}
	}
}

func TestStatusAndMessageFallBackToInternal(t *testing.T) {
	unknown := Code("NOT_A_REAL_CODE")
	if got := Status(unknown); got != http.StatusInternalServerError {
		t.Errorf("Status(unknown) = %d, want 500", got)
	}
	if got := Message(unknown); got != Message(CodeInternal) {
		t.Errorf("Message(unknown) = %q, want the internal error message", got)
	}
}

func run(t *testing.T, method, path, body string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	r := gin.New()
	r.Handle(method, path, handler)

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) ErrorResponse {
	t.Helper()
	var resp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON (%q): %v", w.Body.String(), err)
	}
	return resp
}

func TestFail(t *testing.T) {
	w := run(t, http.MethodPost, "/", "", func(c *gin.Context) {
		Fail(c, CodeInvalidCredentials)
	})

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}

	resp := decode(t, w)
	if resp.Code != CodeInvalidCredentials {
		t.Errorf("code = %q, want %q", resp.Code, CodeInvalidCredentials)
	}

	if resp.Error != "Invalid email or password" {
		t.Errorf("error = %q, want %q", resp.Error, "Invalid email or password")
	}
	if resp.Fields != nil {
		t.Errorf("fields = %v, want none", resp.Fields)
	}
}

func TestFailOmitsFieldsWhenEmpty(t *testing.T) {
	w := run(t, http.MethodPost, "/", "", func(c *gin.Context) {
		Fail(c, CodeInternal)
	})

	if strings.Contains(w.Body.String(), "fields") {
		t.Errorf("body should not contain a fields key: %s", w.Body.String())
	}
}

func TestFailRetryAfter(t *testing.T) {
	w := run(t, http.MethodPost, "/", "", func(c *gin.Context) {
		FailRetryAfter(c, CodeAccountLocked, 15*time.Minute)
	})

	if w.Code != http.StatusLocked {
		t.Errorf("status = %d, want 423", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "900" {
		t.Errorf("Retry-After = %q, want %q", got, "900")
	}
}

func TestFailRetryAfterNeverReportsZero(t *testing.T) {
	w := run(t, http.MethodPost, "/", "", func(c *gin.Context) {
		FailRetryAfter(c, CodeRateLimited, 100*time.Millisecond)
	})

	if got := w.Header().Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After = %q, want %q", got, "1")
	}
}

func TestFailAbortsTheHandlerChain(t *testing.T) {
	r := gin.New()
	reached := false
	r.POST("/", func(c *gin.Context) { Fail(c, CodeForbidden) }, func(c *gin.Context) { reached = true })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", nil))

	if reached {
		t.Error("the next handler ran after Fail")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

type sampleRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=12"`
	FullName string `json:"full_name" binding:"required"`
}

func TestFailValidationReportsFields(t *testing.T) {
	RegisterValidationFieldNames()

	w := run(t, http.MethodPost, "/", `{"email":"not-an-email","password":"short"}`, func(c *gin.Context) {
		var req sampleRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			FailValidation(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}

	resp := decode(t, w)
	if resp.Code != CodeValidationFailed {
		t.Errorf("code = %q, want %q", resp.Code, CodeValidationFailed)
	}

	want := map[string]string{
		"email":     "invalid",
		"password":  "min:12",
		"full_name": "required",
	}
	for field, reason := range want {
		if got := resp.Fields[field]; got != reason {
			t.Errorf("fields[%q] = %q, want %q", field, got, reason)
		}
	}
}

func TestFailValidationOnMalformedJSON(t *testing.T) {
	w := run(t, http.MethodPost, "/", `{"email":`, func(c *gin.Context) {
		var req sampleRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			FailValidation(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}

	resp := decode(t, w)
	if resp.Code != CodeValidationFailed {
		t.Errorf("code = %q, want %q", resp.Code, CodeValidationFailed)
	}
	if len(resp.Fields) != 0 {
		t.Errorf("fields = %v, want none", resp.Fields)
	}
	if resp.Error != Message(CodeValidationFailed) {
		t.Errorf("error = %q, want the generic validation message", resp.Error)
	}
}

func TestValidationFieldsIgnoresOtherErrors(t *testing.T) {
	if fields := ValidationFields(http.ErrBodyNotAllowed); fields != nil {
		t.Errorf("ValidationFields = %v, want nil", fields)
	}
}
