// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	MinPasswordBytes = 12
	MaxPasswordBytes = 1024
)

type PasswordParams struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

var DefaultPasswordParams = PasswordParams{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32}

func HashPassword(password string, params PasswordParams) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	if err := validateParams(params); err != nil {
		return "", err
	}
	salt := make([]byte, params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, params.Iterations, params.Memory, params.Parallelism, params.KeyLength)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, params.Memory, params.Iterations, params.Parallelism, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

func VerifyPassword(encoded, password string) (valid, rehash bool, err error) {
	if len(password) > MaxPasswordBytes {
		return false, false, nil
	}
	params, salt, expected, err := parsePasswordHash(encoded)
	if err != nil {
		return false, false, err
	}
	actual := argon2.IDKey([]byte(password), salt, params.Iterations, params.Memory, params.Parallelism, uint32(len(expected)))
	valid = subtle.ConstantTimeCompare(actual, expected) == 1
	belowOrEqual := params.Memory <= DefaultPasswordParams.Memory && params.Iterations <= DefaultPasswordParams.Iterations && params.Parallelism <= DefaultPasswordParams.Parallelism && params.SaltLength <= DefaultPasswordParams.SaltLength && params.KeyLength <= DefaultPasswordParams.KeyLength
	return valid, valid && belowOrEqual && params != DefaultPasswordParams, nil
}

func validatePassword(password string) error {
	if len(password) < MinPasswordBytes || len(password) > MaxPasswordBytes {
		return errors.New("password must be between 12 and 1024 bytes")
	}
	return nil
}

func validateParams(p PasswordParams) error {
	if p.Memory < 8*1024 || p.Memory > 256*1024 || p.Iterations < 1 || p.Iterations > 10 || p.Parallelism < 1 || p.Parallelism > 16 || p.SaltLength < 16 || p.SaltLength > 64 || p.KeyLength < 16 || p.KeyLength > 64 {
		return errors.New("argon2id parameters outside safe bounds")
	}
	return nil
}

func parsePasswordHash(encoded string) (PasswordParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return PasswordParams{}, nil, nil, errors.New("invalid password hash format")
	}
	version, err := strconv.Atoi(strings.TrimPrefix(parts[2], "v="))
	if err != nil || version != argon2.Version {
		return PasswordParams{}, nil, nil, errors.New("unsupported argon2id version")
	}
	var p PasswordParams
	if _, err = fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return p, nil, nil, errors.New("invalid argon2id parameters")
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return p, nil, nil, errors.New("invalid password salt")
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil {
		return p, nil, nil, errors.New("invalid password key")
	}
	p.SaltLength, p.KeyLength = uint32(len(salt)), uint32(len(key))
	if err := validateParams(p); err != nil {
		return p, nil, nil, err
	}
	return p, salt, key, nil
}
