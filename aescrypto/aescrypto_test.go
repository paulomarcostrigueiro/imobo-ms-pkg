// Copyright 2026 imobo. Licenca: privada.

package aescrypto

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func key32B64(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func mustNew(t *testing.T, raw string) Encryptor {
	t.Helper()
	enc, err := New(raw)
	if err != nil {
		t.Fatalf("New(%q): %v", raw, err)
	}
	return enc
}

func TestRoundtrip_ChaveBase64(t *testing.T) {
	enc := mustNew(t, key32B64(t))
	plain := "senha-do-certificado-A1: çãõ 123!@#"
	ct, err := enc.Encrypt(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if ct == plain || strings.Contains(ct, plain) {
		t.Fatal("ciphertext nao deveria conter o plaintext")
	}
	got, err := enc.Decrypt(ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != plain {
		t.Fatalf("roundtrip: got %q want %q", got, plain)
	}
}

func TestNonceUnico(t *testing.T) {
	enc := mustNew(t, key32B64(t))
	a, _ := enc.Encrypt("x")
	b, _ := enc.Encrypt("x")
	if a == b {
		t.Fatal("dois Encrypt do mesmo plaintext deveriam diferir (nonce aleatorio)")
	}
}

func TestFallbackSHA256(t *testing.T) {
	enc := mustNew(t, "uma-frase-secreta-bem-longa-com-mais-de-32-chars") // >=32 → SHA-256
	ct, err := enc.Encrypt("dado")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	got, err := enc.Decrypt(ct)
	if err != nil || got != "dado" {
		t.Fatalf("fallback roundtrip falhou: got=%q err=%v", got, err)
	}
}

func TestTamperFalha(t *testing.T) {
	enc := mustNew(t, key32B64(t))
	ct, _ := enc.Encrypt("dado")
	raw, _ := base64.StdEncoding.DecodeString(ct)
	raw[len(raw)-1] ^= 0xFF // corrompe o tag GCM
	if _, err := enc.Decrypt(base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("decrypt deveria falhar em ciphertext adulterado (GCM tag)")
	}
}

func TestChaveErradaNaoDecifra(t *testing.T) {
	ct, _ := mustNew(t, key32B64(t)).Encrypt("segredo")
	if _, err := mustNew(t, key32B64(t)).Decrypt(ct); err == nil {
		t.Fatal("decrypt com outra chave deveria falhar")
	}
}

func TestDecryptBase64Invalido(t *testing.T) {
	if _, err := mustNew(t, key32B64(t)).Decrypt("@@@nao-e-base64@@@"); err == nil {
		t.Fatal("decrypt deveria falhar em base64 invalido")
	}
}

func TestDecryptCurtoDemais(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte{1, 2, 3})
	if _, err := mustNew(t, key32B64(t)).Decrypt(short); err == nil {
		t.Fatal("decrypt deveria falhar em ciphertext menor que o nonce")
	}
}

func TestNoopDev(t *testing.T) {
	n := Noop{}
	ct, _ := n.Encrypt("abc")
	if ct != "noop:abc" {
		t.Fatalf("noop encrypt = %q", ct)
	}
	got, _ := n.Decrypt(ct)
	if got != "abc" {
		t.Fatalf("noop decrypt = %q", got)
	}
}

func TestNewVazioErro(t *testing.T) {
	if _, err := New("   "); !errors.Is(err, ErrChaveVazia) {
		t.Fatalf("New(vazio) deveria retornar ErrChaveVazia, got %v", err)
	}
}

func TestNewChaveFracaErro(t *testing.T) {
	if _, err := New("curta-demais"); !errors.Is(err, ErrChaveFraca) {
		t.Fatalf("New(curta) deveria retornar ErrChaveFraca, got %v", err)
	}
}
