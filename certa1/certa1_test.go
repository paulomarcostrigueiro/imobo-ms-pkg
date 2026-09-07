// Copyright 2026 imobo. Licenca: privada.

// Testes de unidade do certa1. O alvo nao e "o pkcs12 funciona" — e travar as
// tres decisoes que custaram caro para descobrir: senha errada tem que dizer
// senha errada, certificado vencido abre mas nao conecta, e nada do segredo
// pode vazar na mensagem de erro.
package certa1

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

const (
	senhaTeste = "senha-do-pfx-de-teste"
	cnpjTeste  = "41402741000186" // CNPJ da IMOBO, DV valido
)

// gerarPFX monta um .pfx autoassinado por uma AC de teste, no formato legado,
// que e o que as ACs brasileiras emitem. Assinar por uma AC separada importa:
// e o que faz o Issuer CN ser diferente do Subject CN, como num certificado de
// verdade, e e o Issuer que o Credencia pede no cadastro.
func gerarPFX(t *testing.T, cn string, validade time.Time) []byte {
	t.Helper()

	chaveAC, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gerar chave da AC: %v", err)
	}
	modeloAC := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "AC TESTE IMOBO"},
		NotBefore:             validade.Add(-2 * 365 * 24 * time.Hour),
		NotAfter:              validade.Add(365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	derAC, err := x509.CreateCertificate(rand.Reader, modeloAC, modeloAC, &chaveAC.PublicKey, chaveAC)
	if err != nil {
		t.Fatalf("criar AC: %v", err)
	}
	ac, err := x509.ParseCertificate(derAC)
	if err != nil {
		t.Fatalf("parsear AC: %v", err)
	}

	chave, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gerar chave: %v", err)
	}
	modelo := &x509.Certificate{
		SerialNumber: big.NewInt(0x0A1B2C),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    validade.Add(-365 * 24 * time.Hour),
		NotAfter:     validade,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	der, err := x509.CreateCertificate(rand.Reader, modelo, ac, &chave.PublicKey, chaveAC)
	if err != nil {
		t.Fatalf("criar certificado: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsear certificado: %v", err)
	}

	pfx, err := pkcs12.LegacyRC2.Encode(chave, cert, []*x509.Certificate{ac}, senhaTeste)
	if err != nil {
		t.Fatalf("montar pfx: %v", err)
	}
	return pfx
}

func amanha() time.Time { return time.Now().UTC().Add(24 * time.Hour) }

func TestAbrir_ExtraiIdentidadeDoCN(t *testing.T) {
	pfx := gerarPFX(t, "REVENDA MODELO LTDA:"+cnpjTeste, amanha())

	info, err := Abrir(pfx, senhaTeste)
	if err != nil {
		t.Fatalf("Abrir: %v", err)
	}
	if info.CNPJ != cnpjTeste {
		t.Fatalf("CNPJ = %q, queria %q", info.CNPJ, cnpjTeste)
	}
	if info.RazaoSocial != "REVENDA MODELO LTDA" {
		t.Fatalf("razao social = %q", info.RazaoSocial)
	}
	if info.EmissorCN != "AC TESTE IMOBO" {
		t.Fatalf("emissor = %q", info.EmissorCN)
	}
	// Serial e thumbprint sao o que o Credencia e a trilha de auditoria pedem.
	if info.Serial == "" || info.Thumbprint == "" {
		t.Fatalf("serial e thumbprint sao obrigatorios: %+v", info)
	}
	if len(info.Thumbprint) != 64 {
		t.Fatalf("thumbprint deveria ser SHA-256 em hex (64 chars), got %d", len(info.Thumbprint))
	}
}

// CN sem CNPJ e sem SAN: falha com causa clara, nao com CNPJ vazio silencioso.
func TestAbrir_SemCNPJIdentificavel(t *testing.T) {
	pfx := gerarPFX(t, "REVENDA SEM CNPJ NO CN", amanha())

	_, err := Abrir(pfx, senhaTeste)
	if err == nil {
		t.Fatal("esperado erro")
	}
	if !strings.Contains(err.Error(), "CNPJ") {
		t.Fatalf("o erro precisa dizer o que faltou, got %q", err)
	}
}

// Um CNPJ com DV errado no CN nao pode ser aceito: 14 digitos quaisquer nao
// sao um CNPJ.
func TestAbrir_RejeitaCNPJComDVErrado(t *testing.T) {
	pfx := gerarPFX(t, "REVENDA MODELO LTDA:11111111111111", amanha())

	if _, err := Abrir(pfx, senhaTeste); err == nil {
		t.Fatal("CNPJ com DV invalido nao pode passar")
	}
}

func TestAbrir_SenhaIncorreta(t *testing.T) {
	pfx := gerarPFX(t, "REVENDA MODELO LTDA:"+cnpjTeste, amanha())

	_, err := Abrir(pfx, "senha-errada")
	if err == nil {
		t.Fatal("esperado erro")
	}
	if !isSenhaIncorreta(err) {
		t.Fatalf("esperado ErrSenhaIncorreta, got %v", err)
	}
}

func TestAbrir_ArquivoInvalido(t *testing.T) {
	for nome, entrada := range map[string][]byte{
		"vazio":    {},
		"lixo":     []byte("isto nao e um pfx"),
		"truncado": {0x30, 0x82, 0x04},
	} {
		if _, err := Abrir(entrada, senhaTeste); !isArquivoInvalido(err) {
			t.Fatalf("%s: esperado ErrArquivoInvalido, got %v", nome, err)
		}
	}
}

// Vencido ABRE, de proposito: a tela precisa da data para dizer "venceu em
// 12/03" em vez de "arquivo invalido".
func TestAbrir_VencidoAindaDevolveMetadados(t *testing.T) {
	ontem := time.Now().UTC().Add(-24 * time.Hour)
	pfx := gerarPFX(t, "REVENDA MODELO LTDA:"+cnpjTeste, ontem)

	info, err := Abrir(pfx, senhaTeste)
	if err != nil {
		t.Fatalf("certificado vencido deve ABRIR: %v", err)
	}
	if !info.Vencido(time.Now().UTC()) {
		t.Fatal("deveria estar marcado como vencido")
	}
}

// Mas nao conecta.
func TestMontarTLS_RecusaVencido(t *testing.T) {
	ontem := time.Now().UTC().Add(-24 * time.Hour)
	pfx := gerarPFX(t, "REVENDA MODELO LTDA:"+cnpjTeste, ontem)

	_, err := MontarTLS(pfx, senhaTeste, Opcoes{})
	if err == nil {
		t.Fatal("esperado erro")
	}
	if !isVencido(err) {
		t.Fatalf("esperado ErrVencido, got %v", err)
	}
}

func TestMontarTLS_MontaCadeiaEMinimoTLS12(t *testing.T) {
	pfx := gerarPFX(t, "REVENDA MODELO LTDA:"+cnpjTeste, amanha())

	cfg, err := MontarTLS(pfx, senhaTeste, Opcoes{})
	if err != nil {
		t.Fatalf("MontarTLS: %v", err)
	}
	if len(cfg.Certificates) != 1 || len(cfg.Certificates[0].Certificate) != 2 {
		t.Fatalf("a cadeia deveria ter folha + AC, got %d", len(cfg.Certificates[0].Certificate))
	}
	if cfg.Certificates[0].PrivateKey == nil {
		t.Fatal("sem chave privada nao ha mTLS")
	}
	if cfg.MinVersion != 0x0303 { // tls.VersionTLS12
		t.Fatalf("minimo deveria ser TLS 1.2, got %x", cfg.MinVersion)
	}
}

// O relogio injetado prova que a recusa e por data, nao por acaso.
func TestMontarTLS_RelogioInjetado(t *testing.T) {
	validade := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	pfx := gerarPFX(t, "REVENDA MODELO LTDA:"+cnpjTeste, validade)

	antes := Opcoes{Agora: func() time.Time { return validade.Add(-time.Hour) }}
	if _, err := MontarTLS(pfx, senhaTeste, antes); err != nil {
		t.Fatalf("uma hora antes de vencer deveria funcionar: %v", err)
	}
	depois := Opcoes{Agora: func() time.Time { return validade.Add(time.Hour) }}
	if _, err := MontarTLS(pfx, senhaTeste, depois); !isVencido(err) {
		t.Fatalf("uma hora depois deveria recusar, got %v", err)
	}
}

// LEI #11. Este e o teste que eu nao abriria mao: nenhuma mensagem de erro
// pode conter a senha nem bytes do arquivo.
func TestErros_NaoVazamSegredo(t *testing.T) {
	pfx := gerarPFX(t, "REVENDA MODELO LTDA:"+cnpjTeste, amanha())
	const senhaSecreta = "esta-senha-nao-pode-aparecer"

	_, err := Abrir(pfx, senhaSecreta)
	if err == nil {
		t.Fatal("esperado erro de senha")
	}
	if strings.Contains(err.Error(), senhaSecreta) {
		t.Fatalf("a senha vazou no erro: %q", err)
	}
	if strings.Contains(err.Error(), string(pfx[:16])) {
		t.Fatalf("bytes do arquivo vazaram no erro: %q", err)
	}
}

func TestCNPJPlausivel(t *testing.T) {
	if !cnpjPlausivel(cnpjTeste) {
		t.Fatal("CNPJ valido deveria passar")
	}
	for _, ruim := range []string{"", "123", "41402741000187", "11111111111111", "abcdefghijklmn"} {
		if cnpjPlausivel(ruim) {
			t.Fatalf("%q nao deveria passar", ruim)
		}
	}
}

func isSenhaIncorreta(err error) bool  { return errors.Is(err, ErrSenhaIncorreta) }
func isArquivoInvalido(err error) bool { return errors.Is(err, ErrArquivoInvalido) }
func isVencido(err error) bool         { return errors.Is(err, ErrVencido) }
