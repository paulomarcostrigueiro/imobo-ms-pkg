// Copyright 2026 imobo. Licenca: privada.

// Package aescrypto e a lib compartilhada de cifra app-level (AES-256-GCM) dos
// segredos dos serviços (cert A1, senhas, tokens) — LEI #11. Capacidade técnica
// compartilhada (LEI-MS #29) → vive em pkg, atrás da interface Encryptor.
//
// # Formato de saída
//
// Desde o CHAVEIRO, o Encrypt emite:
//
//	v2:<keyid>:base64( nonce || ciphertext || tag )
//
// O `keyid` identifica QUAL chave cifrou aquele valor, e é o que torna a
// ROTAÇÃO possível. Antes disso a saída era só o base64, sem marca nenhuma:
// trocar a chave transformava todo segredo gravado em lixo indecifrável, e a
// falha não aparecia no boot — aparecia na primeira operação de cada cliente.
//
// # Retrocompatibilidade
//
// Blob SEM prefixo é tratado como LEGADO e decifrado tentando cada chave do
// chaveiro, na ordem (atual primeiro, depois as anteriores). O GCM autentica,
// então chave errada falha de forma limpa — tentar é seguro e não ambíguo.
// Nada do que já está gravado precisa ser reescrito.
//
// # Como rotacionar
//
// A chave nova entra como ATUAL; a antiga fica no chaveiro só para LEITURA
// (NewKeyring). O que já estava gravado continua abrindo; o que for reescrito
// nasce com a chave nova. Estratégia adotada: deixar ENVELHECER — nada é
// reescrito em massa, cada segredo migra quando o dono o atualizar. Para
// aposentar uma chave de vez é preciso reescrever o que restou dela antes de
// tirá-la do chaveiro.
//
// # Limite conhecido
//
// Ainda NÃO usamos AAD (dado associado) — o ciphertext não é amarrado ao seu
// contexto (tenant, coluna). Deliberado: são duas mudanças de cifra, e esta já
// mexe em pacote que o fiscal usa em produção. Fica para um segundo passo, e
// cabe no mesmo esquema de versão (`v3:`).
package aescrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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

// prefixoV2 marca o formato COM keyid. Base64 padrão não contém ":", então a
// presença do prefixo distingue v2 de legado sem ambiguidade.
const prefixoV2 = "v2:"

// ErrChaveVazia / ErrChaveFraca — falhas explícitas (achado A2/M1): New NUNCA
// degrada silenciosamente para Noop. Em DEV/CI, use aescrypto.Noop{} de propósito.
var (
	ErrChaveVazia = errors.New("aescrypto: chave vazia — em dev/CI use aescrypto.Noop{} explicitamente, NUNCA em prod")
	ErrChaveFraca = errors.New("aescrypto: chave fraca — use base64 de 32 bytes (preferido) ou passphrase >= 32 chars")
	// ErrChaveDesconhecida: o blob aponta um keyid que não está no chaveiro. É
	// o erro de quem tirou uma chave do ar antes de reescrever o que ela cifrou.
	ErrChaveDesconhecida = errors.New("aescrypto: keyid do blob nao esta no chaveiro — a chave que cifrou este valor foi removida cedo demais")
	ErrFormato           = errors.New("aescrypto: formato invalido")
)

// New monta o Encryptor a partir do raw secret (env). NÃO cai em Noop silencioso.
//  1. raw vazio → ErrChaveVazia (force decisão consciente).
//  2. raw = base64 de 32 bytes → uso direto (modo PREFERIDO).
//  3. passphrase >= 32 chars → SHA-256(raw) (fallback determinístico).
//  4. demais → ErrChaveFraca.
//
// ESCREVE NO FORMATO ANTIGO (base64 puro), de propósito: subir o pacote novo
// com a mesma configuração de hoje NÃO muda um byte do que vai para o banco.
// Isso preserva o caminho de volta — o código antigo continua conseguindo ler
// tudo o que for gravado. Ler, o New já lê os dois formatos.
//
// Quem escreve v2 é o NewKeyring, e só se usa chaveiro quando se está
// rotacionando de fato.
func New(raw string) (Encryptor, error) {
	c, err := derivaChave(raw)
	if err != nil {
		return nil, err
	}
	return &aesGCM{anel: []chave{c}, escreveV2: false}, nil
}

// NewKeyring monta o Encryptor com CHAVEIRO: `atual` cifra e decifra;
// `anteriores` só decifram. É o que permite rotacionar sem invalidar o que já
// está gravado — a chave nova entra como `atual` e a velha passa a `anteriores`.
//
// ESCREVE EM v2, porque rotação sem keyid não funciona: é o prefixo que diz
// qual chave abre cada valor. Consequência a saber antes de usar: valor
// gravado em v2 NÃO é legível pelo código anterior a esta versão. Rotacionar
// é, portanto, uma decisão sem volta fácil — diferente de só subir o pacote,
// que com New não muda nada no banco.
//
// Cada chave é validada pelas mesmas regras do New. Chave repetida é rejeitada:
// keyid duplicado no chaveiro é sinal de engano de configuração, não de rotação.
func NewKeyring(atual string, anteriores ...string) (Encryptor, error) {
	principal, err := derivaChave(atual)
	if err != nil {
		return nil, err
	}
	anel := []chave{principal}
	vistos := map[string]bool{principal.id: true}
	for i, bruta := range anteriores {
		c, err := derivaChave(bruta)
		if err != nil {
			return nil, fmt.Errorf("aescrypto: chave anterior #%d: %w", i+1, err)
		}
		if vistos[c.id] {
			return nil, fmt.Errorf("aescrypto: chave anterior #%d repetida (keyid %s) — confira a configuracao", i+1, c.id)
		}
		vistos[c.id] = true
		anel = append(anel, c)
	}
	return &aesGCM{anel: anel, escreveV2: true}, nil
}

// chave é uma entrada do chaveiro: o material de 32 bytes e o seu identificador.
type chave struct {
	id  string
	key []byte
}

// derivaChave aplica as regras do New a UMA chave e calcula o keyid.
func derivaChave(raw string) (chave, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return chave{}, ErrChaveVazia
	}
	key := make([]byte, 32)
	if decoded, err := base64.StdEncoding.DecodeString(s); err == nil && len(decoded) == 32 {
		copy(key, decoded)
	} else {
		if len(s) < minRawKeyLen {
			return chave{}, ErrChaveFraca
		}
		h := sha256.Sum256([]byte(s))
		copy(key, h[:])
	}
	return chave{id: keyID(key), key: key}, nil
}

// keyID identifica a chave sem revelá-la: 8 hex do SHA-256 do MATERIAL da
// chave. Determinístico (a mesma chave sempre gera o mesmo id, em qualquer
// serviço) e não reversível.
func keyID(key []byte) string {
	h := sha256.Sum256(key)
	return hex.EncodeToString(h[:4])
}

type aesGCM struct {
	anel []chave
	// escreveV2 decide o formato de ESCRITA. Leitura sempre aceita os dois.
	// false (New) = formato antigo, byte-compatível com o código anterior.
	// true (NewKeyring) = v2 com keyid, necessário para rotacionar.
	escreveV2 bool
}

// atual é a chave que cifra: a primeira do chaveiro.
func (e *aesGCM) atual() (chave, error) {
	if e == nil || len(e.anel) == 0 {
		return chave{}, errors.New("aescrypto: chaveiro vazio")
	}
	return e.anel[0], nil
}

func gcmDe(c chave) (cipher.AEAD, error) {
	if len(c.key) != 32 {
		return nil, errors.New("aescrypto: chave nao inicializada (256 bits)")
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, fmt.Errorf("aescrypto: aes.NewCipher: %w", err)
	}
	return cipher.NewGCM(block)
}

func (e *aesGCM) Encrypt(plain string) (string, error) {
	c, err := e.atual()
	if err != nil {
		return "", err
	}
	g, err := gcmDe(c)
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
	corpo := base64.StdEncoding.EncodeToString(out)
	if !e.escreveV2 {
		return corpo, nil // formato antigo — o codigo anterior continua lendo
	}
	return prefixoV2 + c.id + ":" + corpo, nil
}

func (e *aesGCM) Decrypt(cipherB64 string) (string, error) {
	if e == nil || len(e.anel) == 0 {
		return "", errors.New("aescrypto: chaveiro vazio")
	}
	s := strings.TrimSpace(cipherB64)

	// Formato v2: o blob diz qual chave o cifrou.
	if strings.HasPrefix(s, prefixoV2) {
		resto := s[len(prefixoV2):]
		i := strings.IndexByte(resto, ':')
		if i <= 0 {
			return "", fmt.Errorf("%w: esperado v2:<keyid>:<base64>", ErrFormato)
		}
		id, corpo := resto[:i], resto[i+1:]
		for _, c := range e.anel {
			if c.id != id {
				continue
			}
			return abre(c, corpo)
		}
		return "", fmt.Errorf("%w (keyid %s)", ErrChaveDesconhecida, id)
	}

	// Legado (sem prefixo): tenta cada chave do chaveiro. O GCM autentica, então
	// chave errada falha limpa — a primeira que abrir é a certa.
	var ultimo error
	for _, c := range e.anel {
		plain, err := abre(c, s)
		if err == nil {
			return plain, nil
		}
		ultimo = err
	}
	return "", fmt.Errorf("aescrypto: legado nao abriu com nenhuma chave do chaveiro: %w", ultimo)
}

// abre decifra o corpo base64 com uma chave específica.
func abre(c chave, corpoB64 string) (string, error) {
	g, err := gcmDe(c)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(corpoB64)
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

// KeyIDAtual expõe o keyid da chave que está cifrando. Serve para diagnóstico e
// para medir o avanço de uma rotação (quanto ainda está na chave velha) sem
// precisar decifrar nada.
func KeyIDAtual(e Encryptor) (string, bool) {
	a, ok := e.(*aesGCM)
	if !ok {
		return "", false
	}
	c, err := a.atual()
	if err != nil {
		return "", false
	}
	return c.id, true
}

// KeyIDDoBlob lê o keyid de um valor cifrado, sem chave nenhuma. Vazio + false
// quando o blob é legado (sem prefixo) — que é justamente o que uma varredura
// de rotação precisa distinguir.
func KeyIDDoBlob(cipherB64 string) (string, bool) {
	s := strings.TrimSpace(cipherB64)
	if !strings.HasPrefix(s, prefixoV2) {
		return "", false
	}
	resto := s[len(prefixoV2):]
	i := strings.IndexByte(resto, ':')
	if i <= 0 {
		return "", false
	}
	return resto[:i], true
}

// Noop NÃO cifra (prefixo "noop:") — apenas DEV/CI. Proibido em produção.
type Noop struct{}

func (Noop) Encrypt(plain string) (string, error) { return "noop:" + plain, nil }
func (Noop) Decrypt(cipherB64 string) (string, error) {
	return strings.TrimPrefix(cipherB64, "noop:"), nil
}
