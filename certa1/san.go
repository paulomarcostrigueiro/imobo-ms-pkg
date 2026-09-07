// Copyright 2026 imobo. Licenca: privada.

// Extracao do CNPJ da extensao SubjectAlternativeName (SAN) de certificados
// ICP-Brasil e-CNPJ.
//
// O CN "RAZAO SOCIAL:CNPJ" e convencao, nao norma — o local CANONICO do CNPJ
// no e-CNPJ (DOC-ICP-04) e o otherName do SAN com OID 2.16.76.1.3.3. Algumas
// ACs emitem certificados SEM o CNPJ no CN; este fallback cobre esses casos.
//
// Estrutura ASN.1 percorrida:
//
//	SubjectAltName ::= SEQUENCE OF GeneralName
//	GeneralName    ::= [0] IMPLICIT OtherName | ...
//	OtherName      ::= SEQUENCE { type-id OID, value [0] EXPLICIT ANY }
//
// O value do OID 2.16.76.1.3.3 carrega o CNPJ (14 digitos), tipicamente como
// OCTET STRING ou PrintableString — extraimos os digitos de forma tolerante.
package certa1

import (
	"crypto/x509"
	"encoding/asn1"
)

var (
	oidExtensaoSAN = asn1.ObjectIdentifier{2, 5, 29, 17}
	oidICPBrasilPJ = asn1.ObjectIdentifier{2, 16, 76, 1, 3, 3} // CNPJ do titular (e-CNPJ)
)

// extrairCNPJDoSAN procura o CNPJ no otherName 2.16.76.1.3.3 do SAN.
// Retorna "" se a extensao/OID nao existir ou nao contiver 14 digitos.
// ExtrairCNPJDoSAN e exportada porque o serial e o CNPJ do titular sao os dois
// metadados que o Credencia usa para vincular o certificado ao estabelecimento.
func extrairCNPJDoSAN(cert *x509.Certificate) string {
	for _, ext := range cert.Extensions {
		if !ext.Id.Equal(oidExtensaoSAN) {
			continue
		}
		var seq asn1.RawValue
		if _, err := asn1.Unmarshal(ext.Value, &seq); err != nil || !seq.IsCompound {
			continue
		}
		rest := seq.Bytes
		for len(rest) > 0 {
			var gn asn1.RawValue
			next, err := asn1.Unmarshal(rest, &gn)
			if err != nil {
				break
			}
			rest = next
			// GeneralName otherName = [0] IMPLICIT, constructed.
			if gn.Class != asn1.ClassContextSpecific || gn.Tag != 0 || !gn.IsCompound {
				continue
			}
			if cnpj := cnpjDoOtherName(gn.Bytes); cnpj != "" {
				return cnpj
			}
		}
	}
	return ""
}

// cnpjDoOtherName decodifica o conteudo de um OtherName (type-id + value) e,
// se o type-id for o OID ICP-Brasil de CNPJ, extrai os 14 digitos do value.
func cnpjDoOtherName(conteudo []byte) string {
	var tipo asn1.ObjectIdentifier
	resto, err := asn1.Unmarshal(conteudo, &tipo)
	if err != nil || !tipo.Equal(oidICPBrasilPJ) {
		return ""
	}
	// value = [0] EXPLICIT ANY — desembrulha o wrapper de contexto.
	var wrapper asn1.RawValue
	if _, err := asn1.Unmarshal(resto, &wrapper); err != nil {
		return ""
	}
	valor := wrapper.Bytes
	// Dentro do wrapper costuma vir uma string ASN.1 (OCTET/Printable/IA5);
	// se decodificar, usa o conteudo; senao usa os bytes crus (tolerancia).
	var inner asn1.RawValue
	if _, err := asn1.Unmarshal(valor, &inner); err == nil && len(inner.Bytes) > 0 {
		valor = inner.Bytes
	}
	cnpj := somenteDigitos(string(valor))
	if !cnpjPlausivel(cnpj) {
		return ""
	}
	return cnpj
}
