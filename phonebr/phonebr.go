// Copyright 2026 imobo. Licenca: privada.

// Package phonebr normaliza e valida telefones brasileiros para o formato E.164
// que a Meta Cloud API aceita: codigo do pais 55 + DDD + numero, SEM "+" e SEM o
// "0" de tronco nacional.
//
// MOTIVACAO: planilhas e bases legadas trazem telefones em formatos variados
// ("051989483791", "(51) 98948-3791", "51989483791", "5551989483791"). A Meta
// NAO entrega quando o formato esta errado e devolve o codigo 131026
// ("undeliverable") como se o numero nao tivesse WhatsApp — mascarando um bug de
// formato como se fosse numero invalido. Centralizar a normalizacao aqui e
// aplica-la no unico ponto de saida (CloudClient.SendMessage) torna a correcao
// uma OBRIGACAO do sistema em qualquer caminho de envio (avulso, agendado, bot,
// 2a via, ativo).
//
// LIMITE HONESTO: este pacote valida apenas o FORMATO (E.164 BR plausivel). Saber
// se o numero realmente tem WhatsApp ativo so e possivel APOS o envio, pelo status
// de entrega (entregue vs falha 131026) — a Meta descontinuou a API sincrona de
// verificacao de contatos.
//
// Zero dependencias externas (apenas stdlib): pode ser importado por qualquer
// camada e binario do ecossistema.
package phonebr

import "strings"

// Status classifica o resultado da validacao de formato de um telefone.
type Status string

const (
	// StatusValido indica numero BR com formato E.164 plausivel (envio permitido).
	StatusValido Status = "valido"
	// StatusSemTelefone indica entrada vazia / sem digitos.
	StatusSemTelefone Status = "sem_telefone"
	// StatusIncompleto indica digitos insuficientes para um numero BR com DDD.
	StatusIncompleto Status = "incompleto"
	// StatusDDDInvalido indica DDD fora da faixa valida brasileira (11-99).
	StatusDDDInvalido Status = "ddd_invalido"
	// StatusFormatoInvalido indica numero que nao casa com nenhum padrao BR conhecido.
	StatusFormatoInvalido Status = "formato_invalido"
)

// Resultado agrega o telefone normalizado + classificacao de formato.
type Resultado struct {
	// E164 e o telefone normalizado (so digitos, com 55) quando Valido; "" caso contrario.
	E164 string
	// Status e a classificacao do formato.
	Status Status
	// Valido e atalho para Status == StatusValido.
	Valido bool
	// Motivo e uma frase curta em PT-BR explicando o problema (vazia quando valido).
	Motivo string
}

// soDigitos remove tudo que nao for digito (0-9).
func soDigitos(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteByte(byte(r))
		}
	}
	return b.String()
}

// dddValido verifica se os 2 primeiros digitos formam um DDD brasileiro valido.
// Faixa oficial: 11..99 com o segundo digito != 0 nao e garantia (ex.: 11, 21),
// entao usamos a lista canonica de DDDs em uso no Brasil.
func dddValido(ddd string) bool {
	if len(ddd) != 2 {
		return false
	}
	switch ddd {
	case
		"11", "12", "13", "14", "15", "16", "17", "18", "19",
		"21", "22", "24", "27", "28",
		"31", "32", "33", "34", "35", "37", "38",
		"41", "42", "43", "44", "45", "46", "47", "48", "49",
		"51", "53", "54", "55",
		"61", "62", "63", "64", "65", "66", "67", "68", "69",
		"71", "73", "74", "75", "77", "79",
		"81", "82", "83", "84", "85", "86", "87", "88", "89",
		"91", "92", "93", "94", "95", "96", "97", "98", "99":
		return true
	default:
		return false
	}
}

// Classify normaliza e valida o FORMATO de um telefone BR.
//
// Regras (apos remover nao-digitos e zeros de tronco a esquerda):
//   - vazio                       -> StatusSemTelefone
//   - 12 ou 13 digitos com "55"   -> ja tem codigo do pais; valida o DDD (pos-55)
//   - 10 digitos (DDD + 8)        -> prefixa 55 (fixo ou celular antigo)
//   - 11 digitos (DDD + 9)        -> prefixa 55 (celular com nono digito)
//   - 8 ou 9 digitos              -> StatusIncompleto (sem DDD, indisparavel)
//   - demais                      -> StatusFormatoInvalido
//
// A Meta ainda ajusta o nono digito do lado dela (numeros antigos registrados
// com 8 digitos), entao NAO exigimos o "9" inicial do celular.
func Classify(raw string) Resultado {
	d := soDigitos(raw)
	d = strings.TrimLeft(d, "0") // remove "0" de tronco nacional
	if d == "" {
		return Resultado{Status: StatusSemTelefone, Motivo: "Sem telefone."}
	}

	switch {
	case len(d) == 12 || len(d) == 13:
		// Esperado: 55 + DDD(2) + numero(8|9). Exige prefixo 55 e DDD valido.
		if !strings.HasPrefix(d, "55") {
			return Resultado{Status: StatusFormatoInvalido,
				Motivo: "Numero com codigo de pais diferente de 55 (nao-BR)."}
		}
		if !dddValido(d[2:4]) {
			return Resultado{Status: StatusDDDInvalido, Motivo: "DDD invalido."}
		}
		return Resultado{E164: d, Status: StatusValido, Valido: true}

	case len(d) == 10 || len(d) == 11:
		// BR sem codigo de pais: DDD(2) + numero(8|9).
		if !dddValido(d[:2]) {
			return Resultado{Status: StatusDDDInvalido, Motivo: "DDD invalido."}
		}
		return Resultado{E164: "55" + d, Status: StatusValido, Valido: true}

	case len(d) >= 8 && len(d) <= 9:
		return Resultado{Status: StatusIncompleto,
			Motivo: "Numero sem DDD — nao da pra disparar."}

	default:
		return Resultado{Status: StatusFormatoInvalido,
			Motivo: "Quantidade de digitos invalida para um telefone BR."}
	}
}

// Normalize devolve o telefone E.164 BR (so digitos, com 55) ou "" se o formato
// nao for plausivel. Atalho idempotente de Classify para o caminho de envio:
// Normalize(Normalize(x)) == Normalize(x).
func Normalize(raw string) string {
	return Classify(raw).E164
}

// Valido informa se o telefone tem formato BR plausivel (envio permitido).
func Valido(raw string) bool {
	return Classify(raw).Valido
}
