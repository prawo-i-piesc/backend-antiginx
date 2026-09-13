package auth

import (
	"bufio"
	_ "embed"
	"errors"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

const (
	MinPasswordLength = 12
	BcryptCost        = 12
)

var (
	ErrPasswordTooShort  = errors.New("auth: password is shorter than the minimum length")
	ErrPasswordTooCommon = errors.New("auth: password is on the common password list")
)

//go:embed data/common_passwords.txt
var commonPasswordList string

var (
	commonPasswordsOnce sync.Once
	commonPasswords     map[string]struct{}
)

func loadCommonPasswords() {
	commonPasswords = make(map[string]struct{})

	scanner := bufio.NewScanner(strings.NewReader(commonPasswordList))
	for scanner.Scan() {
		entry := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if entry == "" || strings.HasPrefix(entry, "#") {
			continue
		}
		commonPasswords[entry] = struct{}{}
	}
}

func IsCommonPassword(password string) bool {
	commonPasswordsOnce.Do(loadCommonPasswords)

	_, found := commonPasswords[strings.ToLower(strings.TrimSpace(password))]
	return found
}

func ValidatePassword(password string) error {
	if len([]rune(password)) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	if IsCommonPassword(password) {
		return ErrPasswordTooCommon
	}
	return nil
}

var dummyHashOnce sync.Once
var dummyHash []byte

func CompareDummyPassword(password string) {
	dummyHashOnce.Do(func() {
		hash, err := bcrypt.GenerateFromPassword([]byte("antiginx-dummy-password"), BcryptCost)
		if err != nil {
			return
		}
		dummyHash = hash
	})

	if len(dummyHash) > 0 {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
	}
}
