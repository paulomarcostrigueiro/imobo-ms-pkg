// Copyright 2026 imobo. Licenca: privada.

// Package veiculobr normaliza, valida e CORRIGE os identificadores de veiculo
// que o RENAVE exige na entrada de estoque: chassi, placa, RENAVAM, numero do
// CRV e codigo de seguranca do CRV.
//
// MOTIVACAO: o lojista nao digita esses campos, ele manda a foto do documento e
// um extrator (OCR ou visao) le. OCR erra sempre nos mesmos pares — 0/O, 1/I,
// 5/S, 8/B, 2/Z — e erra mais em foto de celular de documento plastificado. Uma
// entrada de estoque no RENAVE custa R$ 5,48, e irreversivel na pratica e gera
// responsabilidade solidaria (art. 25 §2o da Resolucao CONTRAN 1.026/2026).
// Mandar um digito errado para o SERPRO e caro e sujo de desfazer. Entao a
// validacao acontece ANTES da chamada, de graca, aqui.
//
// A ideia central e que quase todo campo tem uma RESTRICAO POSICIONAL que torna
// a correcao deterministica, nao um chute:
//
//	chassi   17 caracteres e o VIN da ISO 3779, que NAO admite as letras I, O e Q
//	         justamente para nao confundir com 1 e 0. Entao um "O" lido num chassi
//	         e, com certeza, um zero. Corrigir e seguro.
//	placa    posicoes 1-3 sao sempre letras, 4 sempre digito, 6-7 sempre digitos.
//	         Um "0" na posicao 1 e um "O". Um "O" na posicao 4 e um "0".
//	RENAVAM  11 digitos com digito verificador. Qualquer letra vira digito pela
//	         semelhanca visual, e o DV confere o conjunto.
//
// LIMITE HONESTO, e este e o campo que mais importa: o codigoSegurancaCrv (11
// digitos) e o numeroCrv (12 digitos) NAO tem digito verificador conhecido. Aqui
// so da para conferir tamanho e que sao digitos. Um erro de leitura nesses dois
// campos passa batido por qualquer validacao local e so aparece como recusa do
// SERPRO. Por isso a interface TEM de mostrar esses dois campos para o humano
// confirmar antes de gravar. Nao existe atalho.
//
// Os formatos foram conferidos contra 597 registros reais devolvidos por
// GET /api/estoques da homologacao do RENAVE em 2026-09-07: numeroCrv 12 digitos
// em 524/524, codigoSegurancaCrv 11 digitos em 524/524, chassi 17 caracteres em
// 597/597 e ZERO chassis com I/O/Q, RENAVAM 11 digitos com o DV abaixo conferindo
// em 524 de 524. Os 73 registros restantes eram veiculos sem placa/RENAVAM
// (chassi e o unico campo presente em todos).
//
// Zero dependencias externas (apenas stdlib).
package veiculobr

import "strings"

// Status classifica o resultado da normalizacao de um campo.
type Status string

const (
	// StatusValido indica campo ja no formato correto, sem necessidade de correcao.
	StatusValido Status = "valido"
	// StatusCorrigido indica campo valido APOS correcao deterministica de OCR.
	// O valor devolvido e o corrigido; Correcoes diz o que mudou.
	StatusCorrigido Status = "corrigido"
	// StatusVazio indica entrada sem caracteres uteis.
	StatusVazio Status = "vazio"
	// StatusTamanhoInvalido indica quantidade de caracteres fora do esperado —
	// tipicamente foto cortada ou digito comido pelo reflexo do plastico.
	StatusTamanhoInvalido Status = "tamanho_invalido"
	// StatusCaractereInvalido indica caractere que nao existe naquela posicao e
	// que nao tem correcao deterministica.
	StatusCaractereInvalido Status = "caractere_invalido"
	// StatusDVInvalido indica que os digitos estao no formato certo mas o digito
	// verificador nao fecha. Leitura errada; nao adianta corrigir por conta.
	StatusDVInvalido Status = "dv_invalido"
)

// Correcao registra UMA substituicao aplicada, com o motivo. Serve para a
// interface mostrar ao lojista o que o sistema mexeu e para o log de auditoria.
type Correcao struct {
	// Posicao e o indice base 1 dentro do campo (posicao 1 = primeiro caractere).
	Posicao int
	// De e o caractere lido pelo extrator.
	De rune
	// Para e o caractere corrigido.
	Para rune
	// Motivo explica a regra posicional que tornou a correcao deterministica.
	Motivo string
}

// Resultado agrega o valor normalizado, a classificacao e as correcoes feitas.
type Resultado struct {
	// Valor e o campo normalizado e corrigido quando Valido; "" caso contrario.
	Valor string
	// Original e o que entrou, sem alteracao, para o log.
	Original string
	// Status e a classificacao.
	Status Status
	// Valido e atalho para Status valido ou corrigido.
	Valido bool
	// Correcoes lista as substituicoes aplicadas (vazia quando nada mudou).
	Correcoes []Correcao
	// Detalhe traz uma mensagem curta em portugues para exibir ao usuario quando
	// invalido. Vazio quando valido.
	Detalhe string
}

// PrecisaConferencia indica que o campo passou nas validacoes possiveis mas o
// sistema mexeu nele, ou nao tem como conferir. Nesses casos a interface deve
// pedir a confirmacao do humano antes de gastar a operacao no SERPRO.
func (r Resultado) PrecisaConferencia() bool {
	return r.Status == StatusCorrigido
}

// ---- tabelas de confusao de OCR.
//
// Sao os pares que os motores de OCR trocam entre si em documento brasileiro.
// Cada tabela so e aplicada onde a posicao EXIGE aquele tipo de caractere, o que
// e o que faz a troca ser deterministica em vez de chute.

// paraDigito mapeia letras lidas por engano em posicao que so aceita digito.
var paraDigito = map[rune]rune{
	'O': '0', 'D': '0', 'Q': '0',
	'I': '1', 'L': '1',
	'Z': '2',
	'A': '4',
	'S': '5',
	'G': '6',
	'T': '7',
	'B': '8',
}

// paraLetra mapeia digitos lidos por engano em posicao que so aceita letra.
var paraLetra = map[rune]rune{
	'0': 'O',
	'1': 'I',
	'2': 'Z',
	'4': 'A',
	'5': 'S',
	'6': 'G',
	'7': 'T',
	'8': 'B',
}

func digito(r rune) bool { return r >= '0' && r <= '9' }
func letra(r rune) bool  { return r >= 'A' && r <= 'Z' }

// limpar deixa so A-Z e 0-9, em caixa alta. Remove espaco, ponto, traco e o que
// mais o extrator tiver inventado.
func limpar(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range strings.ToUpper(s) {
		if letra(r) || digito(r) {
			out = append(out, r)
		}
	}
	return string(out)
}

// somenteDigitos mantem so 0-9.
func somenteDigitos(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if digito(r) {
			out = append(out, r)
		}
	}
	return string(out)
}

// exigirDigitos forca todas as posicoes a serem digito, corrigindo letras pela
// tabela de confusao. Devolve o valor, as correcoes e se sobrou algo incorrigivel.
func exigirDigitos(s string, deslocamento int, motivo string) (string, []Correcao, bool) {
	var corr []Correcao
	out := []rune(s)
	for i, r := range out {
		if digito(r) {
			continue
		}
		sub, ok := paraDigito[r]
		if !ok {
			return "", nil, false
		}
		corr = append(corr, Correcao{Posicao: deslocamento + i + 1, De: r, Para: sub, Motivo: motivo})
		out[i] = sub
	}
	return string(out), corr, true
}

// exigirLetras e o espelho de exigirDigitos.
func exigirLetras(s string, deslocamento int, motivo string) (string, []Correcao, bool) {
	var corr []Correcao
	out := []rune(s)
	for i, r := range out {
		if letra(r) {
			continue
		}
		sub, ok := paraLetra[r]
		if !ok {
			return "", nil, false
		}
		corr = append(corr, Correcao{Posicao: deslocamento + i + 1, De: r, Para: sub, Motivo: motivo})
		out[i] = sub
	}
	return string(out), corr, true
}

func montar(original, valor string, corr []Correcao) Resultado {
	st := StatusValido
	if len(corr) > 0 {
		st = StatusCorrigido
	}
	return Resultado{Valor: valor, Original: original, Status: st, Valido: true, Correcoes: corr}
}

func falha(original string, st Status, detalhe string) Resultado {
	return Resultado{Original: original, Status: st, Valido: false, Detalhe: detalhe}
}
