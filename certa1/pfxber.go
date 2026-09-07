// Copyright 2026 imobo. Licenca: privada.

// Normalizacao BER -> DER de arquivos .pfx (PKCS#12) ICP-Brasil.
//
// ORIGEM: este codigo nasceu no fiscal-service
// (internal/infrastructure/sefaz/pfx_ber.go) a partir de um achado em smoke
// E2E com A1 real em 2026-06-09. Foi promovido para o pkg quando o
// renave-service passou a precisar da mesma coisa: duplicar criptografia cria
// duas verdades. O fiscal-service ainda tem a copia dele e deve passar a
// consumir esta.
//
// Contexto (achado do smoke E2E real com A1 ICP-Brasil em 2026-06-09): muitos
// .pfx emitidos por ACs brasileiras (via OpenSSL) usam codificacao BER com
// comprimentos INDEFINIDOS e strings em chunks ("constructed"), enquanto a lib
// go-pkcs12 exige DER estrito ("asn1: indefinite length found (not DER)").
//
// Estrategia:
//  1. reescrever a arvore ASN.1 com comprimentos definidos (DER), fundindo
//     OCTET/BIT STRINGs constructed em primitivas (conteudo preservado byte a
//     byte) — inclusive ciphertext chunked em tags de contexto [N];
//  2. como o conteudo interno do authSafe pode mudar de codificacao, o MAC
//     original deixa de bater; recalculamos o MacData (HMAC-SHA256 + KDF do
//     PKCS#12, RFC 7292 B.2) usando a senha informada — legitimo, pois a
//     normalizacao ocorre em memoria no momento do uso e o MAC so protege
//     contra adulteracao por quem nao conhece a senha.
//
// LEI #11: nenhum byte do certificado ou senha e logado ou incluido em erro.
package certa1

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"fmt"
	"hash"
	"unicode/utf16"

	ber "github.com/go-asn1-ber/asn1-ber"
)

// normalizarPFXBER converte um .pfx BER em DER estrito com MAC recalculado.
// Retorna erro se a estrutura nao parsear como PKCS#12.
func normalizarPFXBER(pfx []byte, senha string) ([]byte, error) {
	pkt, err := ber.DecodePacketErr(pfx)
	if err != nil {
		return nil, fmt.Errorf("certa1: pfx nao parseia como ASN.1: %w", err)
	}
	norm := berParaDER(pkt)
	if len(norm.Children) < 2 {
		return nil, fmt.Errorf("certa1: pfx sem estrutura PKCS#12 (esperado version + authSafe)")
	}

	conteudo := authSafeConteudo(norm.Children[1])
	if conteudo == nil {
		return nil, fmt.Errorf("certa1: pfx sem authSafe reconhecivel")
	}

	macData, err := novoMacData(conteudo, senha)
	if err != nil {
		return nil, err
	}

	novo := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "")
	novo.AppendChild(norm.Children[0])
	novo.AppendChild(norm.Children[1])
	novo.AppendChild(macData)
	return novo.Bytes(), nil
}

// berParaDER reescreve a arvore com comprimentos definidos; strings
// constructed sao fundidas em primitivas (DER exige), preservando o conteudo.
func berParaDER(p *ber.Packet) *ber.Packet {
	if p.TagType == ber.TypeConstructed && p.ClassType == ber.ClassUniversal &&
		(p.Tag == ber.TagOctetString || p.Tag == ber.TagBitString) {
		var buf bytes.Buffer
		coletarConteudo(p, &buf)
		return octetNormalizado(p.ClassType, buf.Bytes())
	}
	// [N] IMPLICIT constructed contendo apenas chunks de OCTET STRING
	// (ex.: encryptedContent chunked) -> funde em primitivo do mesmo tag.
	if p.TagType == ber.TypeConstructed && p.ClassType == ber.ClassContext && soChunksDeOctet(p) {
		var buf bytes.Buffer
		coletarConteudo(p, &buf)
		np := ber.Encode(p.ClassType, ber.TypePrimitive, p.Tag, nil, "")
		np.Data.Write(buf.Bytes())
		return np
	}
	if len(p.Children) == 0 {
		if p.Tag == ber.TagOctetString && p.ClassType == ber.ClassUniversal && p.TagType == ber.TypePrimitive {
			return octetNormalizado(p.ClassType, p.Data.Bytes())
		}
		np := ber.Encode(p.ClassType, p.TagType, p.Tag, nil, "")
		np.Data.Write(p.Data.Bytes())
		return np
	}
	np := ber.Encode(p.ClassType, p.TagType, p.Tag, nil, "")
	for _, c := range p.Children {
		np.AppendChild(berParaDER(c))
	}
	return np
}

// octetNormalizado monta um OCTET STRING primitivo. So tenta normalizar o
// conteudo recursivamente quando ele NAO e DER estrito valido mas parseia
// como BER completo (ex.: SafeContents com comprimentos indefinidos dentro do
// authSafe). Conteudo ja-DER (ou opaco, ex.: ciphertext/IV/salt — que poderia
// "parsear por acaso") e preservado byte a byte. Sem essa guarda, um IV
// aleatorio que parseasse como ASN.1 seria reescrito e corromperia a decifra.
func octetNormalizado(classe ber.Class, conteudo []byte) *ber.Packet {
	np := ber.Encode(classe, ber.TypePrimitive, ber.TagOctetString, nil, "")
	if derEstritoValido(conteudo) {
		np.Data.Write(conteudo)
		return np
	}
	inner, err := ber.DecodePacketErr(conteudo)
	if err == nil && len(inner.Bytes()) >= len(conteudo)-2 {
		np.Data.Write(berParaDER(inner).Bytes())
	} else {
		np.Data.Write(conteudo)
	}
	return np
}

// derEstritoValido verifica se b e uma estrutura ASN.1 DER completa segundo o
// parser estrito da stdlib (que rejeita comprimentos indefinidos).
func derEstritoValido(b []byte) bool {
	var raw asn1.RawValue
	rest, err := asn1.Unmarshal(b, &raw)
	return err == nil && len(rest) == 0
}

func soChunksDeOctet(p *ber.Packet) bool {
	// Exige 2+ filhos: [0] constructed com UM unico OCTET STRING e o wrapper
	// EXPLICIT normal do PKCS#12 (nao chunking) — fundi-lo removeria o header
	// do octet string e corromperia a estrutura (bug pego pelo round-trip).
	if len(p.Children) < 2 {
		return false
	}
	for _, c := range p.Children {
		if !(c.ClassType == ber.ClassUniversal && c.TagType == ber.TypePrimitive && c.Tag == ber.TagOctetString) {
			return false
		}
	}
	return true
}

func coletarConteudo(p *ber.Packet, buf *bytes.Buffer) {
	if len(p.Children) == 0 {
		buf.Write(p.Data.Bytes())
		return
	}
	for _, c := range p.Children {
		coletarConteudo(c, buf)
	}
}

// authSafeConteudo extrai os content octets do ContentInfo do authSafe
// (SEQUENCE{ oid, [0] EXPLICIT OCTET STRING }).
func authSafeConteudo(contentInfo *ber.Packet) []byte {
	for _, c := range contentInfo.Children {
		if c.ClassType != ber.ClassContext {
			continue
		}
		if len(c.Children) == 1 {
			return c.Children[0].Data.Bytes()
		}
		return c.Data.Bytes()
	}
	return nil
}

// oidSHA256DER e o AlgorithmIdentifier OID 2.16.840.1.101.3.4.2.1 ja em DER.
var oidSHA256DER = []byte{0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x01}

const (
	macIteracoes  = 2048
	macSaltLen    = 8
	macKeyLen     = 32
	macIDChaveKDF = 3 // RFC 7292 B.3: ID=3 deriva chave de MAC
)

// novoMacData calcula MacData (HMAC-SHA256) sobre o conteudo normalizado.
func novoMacData(conteudo []byte, senha string) (*ber.Packet, error) {
	salt := make([]byte, macSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("certa1: gerando salt do MAC: %w", err)
	}
	chave := kdfPKCS12(sha256.New, senha, salt, macIteracoes, macIDChaveKDF, macKeyLen)
	m := hmac.New(sha256.New, chave)
	m.Write(conteudo)
	digest := m.Sum(nil)

	algoOID, err := ber.DecodePacketErr(oidSHA256DER)
	if err != nil {
		return nil, fmt.Errorf("certa1: oid sha256 interno invalido: %w", err)
	}
	algo := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "")
	algo.AppendChild(algoOID)
	algo.AppendChild(ber.Encode(ber.ClassUniversal, ber.TypePrimitive, ber.TagNULL, nil, ""))

	digestInfo := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "")
	digestInfo.AppendChild(algo)
	od := ber.Encode(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, nil, "")
	od.Data.Write(digest)
	digestInfo.AppendChild(od)

	macData := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "")
	macData.AppendChild(digestInfo)
	sl := ber.Encode(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, nil, "")
	sl.Data.Write(salt)
	macData.AppendChild(sl)
	macData.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, int64(macIteracoes), ""))
	return macData, nil
}

// kdfPKCS12 implementa a derivacao de chave do PKCS#12 (RFC 7292, Apendice
// B.2) para HMAC (id=3). v=64 (block size do SHA-256), senha em BMPString.
func kdfPKCS12(h func() hash.Hash, senha string, salt []byte, iteracoes, id, n int) []byte {
	const v = 64
	u16 := utf16.Encode([]rune(senha))
	pass := make([]byte, 0, len(u16)*2+2)
	for _, c := range u16 {
		pass = append(pass, byte(c>>8), byte(c))
	}
	pass = append(pass, 0, 0)

	repetir := func(b []byte) []byte {
		if len(b) == 0 {
			return nil
		}
		out := make([]byte, ((len(b)+v-1)/v)*v)
		for i := range out {
			out[i] = b[i%len(b)]
		}
		return out
	}
	diversificador := bytes.Repeat([]byte{byte(id)}, v)
	bufI := append(repetir(salt), repetir(pass)...)

	var out []byte
	for len(out) < n {
		hh := h()
		hh.Write(diversificador)
		hh.Write(bufI)
		a := hh.Sum(nil)
		for i := 1; i < iteracoes; i++ {
			hh = h()
			hh.Write(a)
			a = hh.Sum(nil)
		}
		out = append(out, a...)

		b := make([]byte, v)
		for i := range b {
			b[i] = a[i%len(a)]
		}
		for j := 0; j+v <= len(bufI); j += v {
			carry := 1
			for k := v - 1; k >= 0; k-- {
				soma := int(bufI[j+k]) + int(b[k]) + carry
				bufI[j+k] = byte(soma)
				carry = soma >> 8
			}
		}
	}
	return out[:n]
}
