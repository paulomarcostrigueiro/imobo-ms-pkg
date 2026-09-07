// Copyright 2026 imobo. Licenca: privada.

// Package certa1 abre o certificado e-CNPJ A1 (arquivo .pfx/.p12 ICP-Brasil),
// extrai os metadados nao-secretos e monta a tls.Config de mTLS.
//
// Existe porque tres coisas que parecem simples nao sao, e todas foram
// descobertas com certificado real, nao em teoria:
//
//  1. Certificados ICP-Brasil sao tipicamente LEGACY-encrypted (RC2/3DES). A
//     stdlib nao abre; o go-pkcs12 abre.
//  2. Muitos .pfx de AC brasileira vem em BER com comprimento indefinido, e o
//     go-pkcs12 exige DER estrito ("asn1: indefinite length found (not DER)").
//     Ver pfxber.go.
//  3. O CNPJ nem sempre esta no CN. O lugar canonico e o otherName
//     2.16.76.1.3.3 do SAN. Ver san.go.
//
// LEI #11: nem os bytes do .pfx nem a senha aparecem em log ou em mensagem de
// erro. So metadados nao-secretos saem daqui: CNPJ, razao social, validade,
// serial e thumbprint.
package certa1

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

// Sentinels. Caller usa errors.Is; a mensagem ao usuario final e dele.
var (
	// ErrArquivoInvalido — .pfx vazio, corrompido, sem certificado, ou sem
	// CNPJ identificavel.
	ErrArquivoInvalido = errors.New("certa1: arquivo de certificado invalido")

	// ErrSenhaIncorreta — a senha nao abre o arquivo.
	ErrSenhaIncorreta = errors.New("certa1: senha do certificado incorreta")

	// ErrVencido — o certificado passou da validade. Devolvido apenas por
	// MontarTLS; Abrir devolve os metadados mesmo vencido, de proposito, para
	// a tela poder mostrar a data.
	ErrVencido = errors.New("certa1: certificado vencido")
)

// Info sao os metadados NAO-SECRETOS do certificado.
type Info struct {
	CNPJ        string
	RazaoSocial string
	Validade    time.Time
	// Serial identifica o certificado dentro da AC emissora. E um dos tres
	// dados que o Credencia pede para vincular o certificado ao
	// estabelecimento (serial, common name, CN do emissor).
	Serial string
	// Thumbprint e o SHA-256 do DER. Identidade global, independe da AC. E o
	// que registrar na trilha de auditoria para responder "qual certificado
	// foi usado nesta operacao".
	Thumbprint string
	// EmissorCN e o CN da AC emissora, o terceiro dado do cadastro no
	// Credencia.
	EmissorCN string
}

// Vencido informa se o certificado ja passou da validade em `agora`.
func (i Info) Vencido(agora time.Time) bool { return agora.After(i.Validade) }

// Abrir valida o .pfx com a senha e extrai os metadados.
//
// Devolve os metadados MESMO se o certificado estiver vencido: a recusa por
// vencimento acontece em MontarTLS, na hora de usar. Isso permite a tela
// dizer "seu certificado venceu em 12/03" em vez de "arquivo invalido".
func Abrir(pfx []byte, senha string) (Info, error) {
	_, cert, _, err := decodificar(pfx, senha)
	if err != nil {
		return Info{}, err
	}
	razao, cnpj := identidade(cert)
	if cnpj == "" {
		return Info{}, fmt.Errorf("%w: nao foi possivel extrair o CNPJ do titular", ErrArquivoInvalido)
	}
	return Info{
		CNPJ:        cnpj,
		RazaoSocial: razao,
		Validade:    cert.NotAfter,
		Serial:      serialHex(cert),
		Thumbprint:  thumbprintSHA256(cert),
		EmissorCN:   cert.Issuer.CommonName,
	}, nil
}

// Opcoes de MontarTLS.
type Opcoes struct {
	// Agora permite injetar relogio no teste. Nil = time.Now().UTC().
	Agora func() time.Time
	// RootCAs verifica o certificado do SERVIDOR. Nil = pool do sistema.
	//
	// Para o RENAVE-WS o pool do sistema basta: verificamos ao vivo que os
	// servidores do SERPRO apresentam cadeia Let's Encrypt, nao ICP-Brasil.
	// Servicos que falam com webservice ancorado na ICP-Brasil (varias SEFAZ)
	// precisam passar um pool com essas raizes.
	RootCAs *x509.CertPool
}

// MontarTLS monta a tls.Config de mTLS com a chave privada em memoria.
// Recusa certificado vencido (ErrVencido).
func MontarTLS(pfx []byte, senha string, opts Opcoes) (*tls.Config, error) {
	agora := opts.Agora
	if agora == nil {
		agora = func() time.Time { return time.Now().UTC() }
	}
	chave, cert, cas, err := decodificar(pfx, senha)
	if err != nil {
		return nil, err
	}
	if agora().After(cert.NotAfter) {
		return nil, fmt.Errorf("%w em %s", ErrVencido, cert.NotAfter.Format("02/01/2006"))
	}
	cadeia := make([][]byte, 0, 1+len(cas))
	cadeia = append(cadeia, cert.Raw)
	for _, ca := range cas {
		cadeia = append(cadeia, ca.Raw)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: cadeia,
			PrivateKey:  chave,
			Leaf:        cert,
		}},
		RootCAs:    opts.RootCAs,
		MinVersion: tls.VersionTLS12,
	}, nil
}

// decodificar abre o .pfx, com o fallback BER, e mapeia os erros do pkcs12
// para os sentinels. NUNCA inclui senha ou bytes do arquivo na mensagem.
func decodificar(pfx []byte, senha string) (chave any, cert *x509.Certificate, cas []*x509.Certificate, err error) {
	if len(pfx) == 0 {
		return nil, nil, nil, fmt.Errorf("%w: arquivo vazio", ErrArquivoInvalido)
	}
	chave, cert, cas, err = pkcs12.DecodeChain(pfx, senha)
	if err != nil && !errors.Is(err, pkcs12.ErrIncorrectPassword) {
		// Fallback BER. ATENCAO: normalizarPFXBER RECALCULA o MacData com a
		// senha informada, o que ANULA a deteccao de senha-errada via MAC. Ou
		// seja: com senha INCORRETA num .pfx BER, o MAC "passa" (foi
		// recalculado) e a falha reaparece na decifra dos bags como erro
		// generico, que viraria "arquivo invalido" — mensagem enganosa. Por
		// isso: se a estrutura NORMALIZA (PKCS#12 integro) mas o DecodeChain
		// ainda falha, a causa e quase certamente senha incorreta.
		if der, nErr := normalizarPFXBER(pfx, senha); nErr == nil {
			chave, cert, cas, err = pkcs12.DecodeChain(der, senha)
			if err != nil && !errors.Is(err, pkcs12.ErrIncorrectPassword) {
				err = pkcs12.ErrIncorrectPassword
			}
		}
	}
	if err != nil {
		if errors.Is(err, pkcs12.ErrIncorrectPassword) {
			return nil, nil, nil, ErrSenhaIncorreta
		}
		return nil, nil, nil, fmt.Errorf("%w: falha ao abrir o arquivo", ErrArquivoInvalido)
	}
	if cert == nil {
		return nil, nil, nil, fmt.Errorf("%w: sem certificado dentro do arquivo", ErrArquivoInvalido)
	}
	return chave, cert, cas, nil
}

// identidade tira razao social e CNPJ do certificado. Tenta o CN no formato
// ICP-Brasil "RAZAO SOCIAL LTDA:41402741000186" e, se nao houver CNPJ ali,
// cai para o SAN, que e o lugar canonico.
func identidade(cert *x509.Certificate) (razao, cnpj string) {
	cn := strings.TrimSpace(cert.Subject.CommonName)
	if idx := strings.LastIndex(cn, ":"); idx >= 0 {
		c := somenteDigitos(cn[idx+1:])
		if cnpjPlausivel(c) {
			return strings.TrimSpace(cn[:idx]), c
		}
	}
	if c := extrairCNPJDoSAN(cert); c != "" {
		return cn, c
	}
	return cn, ""
}

// serialHex devolve o numero de serie em hexadecimal uppercase.
func serialHex(cert *x509.Certificate) string {
	if cert == nil || cert.SerialNumber == nil {
		return ""
	}
	return strings.ToUpper(cert.SerialNumber.Text(16))
}

// thumbprintSHA256 devolve o SHA-256 do DER em hexadecimal uppercase.
func thumbprintSHA256(cert *x509.Certificate) string {
	if cert == nil || len(cert.Raw) == 0 {
		return ""
	}
	sum := sha256.Sum256(cert.Raw)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}
