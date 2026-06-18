// Copyright 2026 imobo. Licenca: privada.

// Package aescrypto e a lib compartilhada de cifra app-level (AES-256-GCM) dos
// segredos dos serviços (cert A1, senhas, tokens) — LEI #11. Capacidade técnica
// compartilhada (LEI-MS #29) → vive em pkg, atrás da interface Encryptor.
// Output do Encrypt: base64( nonce || ciphertext || tag ).
package aescrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Encryptor abstrai a cifra dos segredos. Assinatura mínima e estável —
// qualquer serviço a injeta por DI (port hexagonal).
type Encryptor interface {
	Encrypt(plain string) (string, error)
	Decrypt(cipherB64 string) (string, error)
}

// minRawKeyLen é o mínimo de entropia aceito no fallback SHA-256 (achado M1 —
// não aceitar passphrase curta como chave AES-256).
const minRawKeyLen = 32

// ErrChaveVazia / ErrChaveFraca — falhas explícitas (achado A2/M1): New NUNCA
// degrada silenciosamente para Noop. Em DEV/CI, use aescrypto.Noop{} de propósito.
var (
	ErrChaveVazia = errors.New("aescrypto: chave vazia — em dev/CI use aescrypto.Noop{} explicitamente, NUNCA em prod")
	ErrChaveFraca = errors.New("aescrypto: chave fraca — use base64 de 32 bytes (preferido) ou passphrase >= 32 chars")
)

// New monta o Encryptor a partir do raw secret (env). NÃO cai em Noop silencioso.
//  1. raw vazio → ErrChaveVazia (force decisão consciente).
//  2. raw = base64 de 32 bytes → uso direto (modo PREFERIDO).
//  3. passphrase >= 32 chars → SHA-256(raw) (fallback determinístico).
//  4. demais → ErrChaveFraca.
func New(raw string) (Encryptor, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, ErrChaveVazia
	}
	if decoded, err := base64.StdEncoding.DecodeString(s); err == nil && len(decoded) == 32 {
		key := make([]byte, 32)
		copy(key, decoded)
		return &aesGCM{key: key}, nil
	}
	if len(s) < minRawKeyLen {
		return nil, ErrChaveFraca
	}
	h := sha256.Sum256([]byte(s))
	key := make([]byte, 32)
	copy(key, h[:])
	return &aesGCM{key: key}, nil
}

type aesGCM struct{ key []byte }

func (e *aesGCM) gcm() (cipher.AEAD, error) {
	if e == nil || len(e.key) != 32 {
		return nil, errors.New("aescrypto: chave nao inicializada (256 bits)")
	}
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, fmt.Errorf("aescrypto: aes.NewCipher: %w", err)
	}
	return cipher.NewGCM(block)
}

func (e *aesGCM) Encrypt(plain string) (string, error) {
	g, err := e.gcm()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("aescrypto: nonce: %w", err)
	}
	ct := g.Seal(nil, nonce, []byte(plain), nil)
	out := make([]byte, 0, len(nonce)+len(ct))
	out = append(out, nonce...)
	out = append(out, ct...)
	return base64.StdEncoding.EncodeToString(out), nil
}

func (e *aesGCM) Decrypt(cipherB64 string) (string, error) {
	g, err := e.gcm()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(cipherB64)
	if err != nil {
		return "", fmt.Errorf("aescrypto: base64: %w", err)
	}
	ns := g.NonceSize()
	if len(raw) < ns+1 {
		return "", errors.New("aescrypto: ciphertext curto demais")
	}
	nonce, ct := raw[:ns], raw[ns:]
	plain, err := g.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("aescrypto: open (tamper/chave errada): %w", err)
	}
	return string(plain), nil
}

// Noop NÃO cifra (prefixo "noop:") — apenas DEV/CI. Proibido em produção.
type Noop struct{}

func (Noop) Encrypt(plain string) (string, error) { return "noop:" + plain, nil }
func (Noop) Decrypt(cipherB64 string) (string, error) {
	return strings.TrimPrefix(cipherB64, "noop:"), nil
}
