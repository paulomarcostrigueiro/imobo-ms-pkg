// Copyright 2026 imobo. Licenca: privada.

package aescrypto

import (
	"crypto/aes"
	"crypto/cipher"
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

// partesV2 separa "v2:<keyid>:<corpo>" — os testes precisam mexer no corpo sem
// perder o cabeçalho.
func partesV2(t *testing.T, ct string) (cabecalho, corpo string) {
	t.Helper()
	i := strings.LastIndexByte(ct, ':')
	if !strings.HasPrefix(ct, "v2:") || i <= 2 {
		t.Fatalf("esperado formato v2:<keyid>:<base64>, got %q", ct)
	}
	return ct[:i+1], ct[i+1:]
}

func mustKeyring(t *testing.T, atual string, anteriores ...string) Encryptor {
	t.Helper()
	enc, err := NewKeyring(atual, anteriores...)
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	return enc
}

func TestTamperFalha(t *testing.T) {
	enc := mustNew(t, key32B64(t)) // formato antigo: o ct e o base64 puro
	ct, _ := enc.Encrypt("dado")
	raw, err := base64.StdEncoding.DecodeString(ct)
	if err != nil {
		t.Fatalf("ct nao e base64: %v", err)
	}
	raw[len(raw)-1] ^= 0xFF // corrompe o tag GCM
	if _, err := enc.Decrypt(base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("decrypt deveria falhar em ciphertext adulterado (GCM tag)")
	}
}

func TestTamperFalhaV2(t *testing.T) {
	enc := mustKeyring(t, key32B64(t))
	ct, _ := enc.Encrypt("dado")
	cab, corpo := partesV2(t, ct)
	raw, err := base64.StdEncoding.DecodeString(corpo)
	if err != nil {
		t.Fatalf("corpo nao e base64: %v", err)
	}
	raw[len(raw)-1] ^= 0xFF
	if _, err := enc.Decrypt(cab + base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("decrypt deveria falhar em v2 adulterado (GCM tag)")
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

// ---------------------------------------------------------------------------
// CHAVEIRO / ROTAÇÃO — a razão de existir do formato v2.
// ---------------------------------------------------------------------------

// cifraLegado produz um blob no formato ANTIGO (base64 puro, sem prefixo), do
// jeito que está gravado hoje nas 8 colunas *_encrypted em produção. É o dado
// que a rotação NÃO pode quebrar.
func cifraLegado(t *testing.T, chaveB64, plain string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(chaveB64)
	if err != nil || len(raw) != 32 {
		t.Fatalf("chave de teste invalida")
	}
	blk, err := aes.NewCipher(raw)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	g, err := cipher.NewGCM(blk)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("nonce: %v", err)
	}
	ct := g.Seal(nil, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(append(nonce, ct...))
}

// A GARANTIA QUE PROTEGE O QUE JA ESTA NO AR: subir o pacote novo com a mesma
// configuracao de hoje nao muda um byte do que vai para o banco, e o codigo
// anterior continua conseguindo ler o que for gravado.
func TestNewEscreveNoFormatoAntigo(t *testing.T) {
	k := key32B64(t)
	ct, err := mustNew(t, k).Encrypt("segredo")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if strings.HasPrefix(ct, "v2:") {
		t.Fatalf("New NAO pode escrever v2 — quebraria o rollback. got %q", ct)
	}
	if _, ok := KeyIDDoBlob(ct); ok {
		t.Fatal("blob do New nao deveria ter keyid")
	}
	// Prova de compatibilidade: o formato antigo e base64 puro de nonce||ct||tag.
	raw, err := base64.StdEncoding.DecodeString(ct)
	if err != nil {
		t.Fatalf("formato antigo deveria ser base64 puro: %v", err)
	}
	if len(raw) < 12+16 {
		t.Fatalf("blob curto demais para nonce+tag: %d bytes", len(raw))
	}
}

func TestEncryptEmiteFormatoV2(t *testing.T) {
	enc := mustKeyring(t, key32B64(t))
	ct, err := enc.Encrypt("dado")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !strings.HasPrefix(ct, "v2:") {
		t.Fatalf("NewKeyring deveria emitir v2:<keyid>:..., got %q", ct)
	}
	id, ok := KeyIDDoBlob(ct)
	if !ok || len(id) != 8 {
		t.Fatalf("KeyIDDoBlob = %q,%v — esperado 8 hex", id, ok)
	}
	atual, ok := KeyIDAtual(enc)
	if !ok || atual != id {
		t.Fatalf("keyid do blob (%s) deveria ser o da chave atual (%s)", id, atual)
	}
}

// O dado gravado ANTES do chaveiro precisa continuar abrindo. Sem isso a
// mudança quebraria os certificados A1 de todos os clientes do fiscal.
func TestLegadoSemPrefixoAindaDecifra(t *testing.T) {
	k := key32B64(t)
	legado := cifraLegado(t, k, "certificado-antigo")
	if _, ok := KeyIDDoBlob(legado); ok {
		t.Fatal("blob legado nao deveria ter keyid")
	}
	got, err := mustNew(t, k).Decrypt(legado)
	if err != nil || got != "certificado-antigo" {
		t.Fatalf("legado nao abriu: got=%q err=%v", got, err)
	}
}

// O caso que motivou tudo: trocar a chave sem invalidar o que já está gravado.
func TestRotacao_ChaveNovaLeOAntigo(t *testing.T) {
	velha, nova := key32B64(t), key32B64(t)

	antes, err := mustKeyring(t, velha).Encrypt("segredo-do-cliente") // v2 da chave velha
	if err != nil {
		t.Fatalf("encrypt com a chave velha: %v", err)
	}
	legado := cifraLegado(t, velha, "segredo-pre-chaveiro")

	// Rotação: a nova entra como atual, a velha fica só para leitura.
	rot, err := NewKeyring(nova, velha)
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}

	if got, err := rot.Decrypt(antes); err != nil || got != "segredo-do-cliente" {
		t.Fatalf("v2 cifrado com a chave velha deveria abrir: got=%q err=%v", got, err)
	}
	if got, err := rot.Decrypt(legado); err != nil || got != "segredo-pre-chaveiro" {
		t.Fatalf("legado da chave velha deveria abrir: got=%q err=%v", got, err)
	}

	// O que for reescrito nasce com a chave NOVA — é assim que a rotação avança.
	depois, err := rot.Encrypt("regravado")
	if err != nil {
		t.Fatalf("encrypt apos rotacao: %v", err)
	}
	idNovo, _ := KeyIDDoBlob(depois)
	idVelho, _ := KeyIDDoBlob(antes)
	if idNovo == idVelho {
		t.Fatal("apos rotacao o Encrypt deveria usar a chave nova")
	}
	if atual, _ := KeyIDAtual(rot); atual != idNovo {
		t.Fatalf("keyid do novo blob (%s) deveria ser o da chave atual (%s)", idNovo, atual)
	}
}

// Tirar a chave do chaveiro antes de reescrever o que ela cifrou é o erro que
// perde dado. O erro precisa dizer isso, não "chave errada".
func TestChaveRemovidaCedoDemais(t *testing.T) {
	velha, nova := key32B64(t), key32B64(t)
	antes, _ := mustKeyring(t, velha).Encrypt("segredo") // v2, com o keyid da velha

	so, err := NewKeyring(nova) // a velha ficou de fora
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	_, err = so.Decrypt(antes)
	if !errors.Is(err, ErrChaveDesconhecida) {
		t.Fatalf("esperado ErrChaveDesconhecida, got %v", err)
	}
}

func TestChaveiroRejeitaChaveRepetida(t *testing.T) {
	k := key32B64(t)
	if _, err := NewKeyring(k, k); err == nil {
		t.Fatal("chave repetida no chaveiro deveria ser rejeitada (engano de configuracao)")
	}
}

func TestChaveiroValidaAnteriores(t *testing.T) {
	if _, err := NewKeyring(key32B64(t), "curta"); !errors.Is(err, ErrChaveFraca) {
		t.Fatalf("chave anterior fraca deveria ser rejeitada, got %v", err)
	}
}

func TestV2MalFormado(t *testing.T) {
	if _, err := mustNew(t, key32B64(t)).Decrypt("v2:semdoispontos"); !errors.Is(err, ErrFormato) {
		t.Fatal("v2 sem o segundo ':' deveria retornar ErrFormato")
	}
}

// keyid é derivado do MATERIAL da chave: determinístico entre processos e
// serviços, e não revela a chave.
func TestKeyIDEstavelENaoRevelaChave(t *testing.T) {
	k := key32B64(t)
	a, _ := KeyIDAtual(mustNew(t, k))
	b, _ := KeyIDAtual(mustNew(t, k))
	if a != b {
		t.Fatalf("keyid deveria ser estavel para a mesma chave: %s vs %s", a, b)
	}
	if strings.Contains(k, a) {
		t.Fatal("keyid nao pode ser um pedaco da chave")
	}
	if outro, _ := KeyIDAtual(mustNew(t, key32B64(t))); outro == a {
		t.Fatal("chaves diferentes deveriam ter keyids diferentes")
	}
}
