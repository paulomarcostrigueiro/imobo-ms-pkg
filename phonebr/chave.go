// Copyright 2026 imobo. Licenca: privada.

package phonebr

import "strings"

// chave.go e a FUNCAO UNICA de comparacao de telefone BR para DEDUPLICACAO
// (kanban e comunicacao usam a mesma). Caso real que motivou (23/10/2026): a
// pessoa digitou (11) 94405-2300 na landing e mandou o WhatsApp do numero real
// (11) 94005-2300 — dois cards, porque a comparacao era "digitos iguais".
//
// Tres niveis, do mais estrito ao mais solto:
//
//   - ChaveLocal: a chave de ARMAZENAMENTO de sempre (DDD + numero, sem o 55),
//     agora tambem sem o "0" de tronco e sem o codigo de operadora. NAO mexe no
//     nono digito: as chaves ja gravadas continuam casando.
//   - Chave: o E.164 BR COMPARAVEL (55 + DDD + numero), com o nono digito do
//     celular sempre presente. Dois telefones sao o MESMO quando a Chave bate.
//   - Parecido: as Chaves diferem em no maximo 1 digito (troca, falta ou sobra).
//     Serve SO para o aviso "Possivel duplicado" — nunca para juntar sozinho.
//
// Variantes devolve as chaves LOCAIS equivalentes (com e sem o nono digito) para
// a busca no banco achar o que foi gravado de qualquer um dos dois jeitos.

// digitosBR tira tudo que nao e digito, os zeros de tronco a esquerda e o codigo
// de operadora do DDD ("0 15 11 94005-2300" => "11940052300"). O codigo de pais
// 55 fica (quem chama decide).
func digitosBR(raw string) string {
	d := soDigitos(raw)
	tronco := strings.HasPrefix(d, "0")
	d = strings.TrimLeft(d, "0")
	// "+55 0 11 ..." (DDI seguido do tronco): o zero sai.
	if strings.HasPrefix(d, "550") {
		d = "55" + strings.TrimLeft(d[2:], "0")
	}
	// Discagem de longa distancia: 0 + operadora(2) + DDD(2) + numero(8|9).
	if tronco && (len(d) == 12 || len(d) == 13) && !strings.HasPrefix(d, "55") && dddValido(d[2:4]) {
		d = d[2:]
	}
	return d
}

// ChaveLocal e a chave de ARMAZENAMENTO do telefone: so digitos, DDD + numero,
// SEM o codigo do pais 55 quando ele vem (12-13 digitos), sem zero de tronco e
// sem codigo de operadora. Nao insere nem tira o nono digito. Vazio => "".
func ChaveLocal(raw string) string {
	d := digitosBR(raw)
	if (len(d) == 12 || len(d) == 13) && strings.HasPrefix(d, "55") {
		d = d[2:]
	}
	return d
}

// Chave e o E.164 BR COMPARAVEL (so digitos, 55 + DDD + numero) com o nono
// digito do celular sempre presente: celular antigo de 8 digitos (comeca com 6,
// 7, 8 ou 9) ganha o 9. "" quando nao da para afirmar um telefone BR com DDD
// (sem DDD, DDD inexistente, quantidade errada de digitos).
func Chave(raw string) string { return ChaveComDDD(raw, "") }

// ChaveComDDD e a Chave, completando o DDD quando o numero veio SEM ele (8 ou 9
// digitos). dddPadrao invalido ou vazio => numero sem DDD continua "".
func ChaveComDDD(raw, dddPadrao string) string {
	l := ChaveLocal(raw)
	if (len(l) == 8 || len(l) == 9) && dddValido(dddPadrao) {
		l = dddPadrao + l
	}
	if len(l) != 10 && len(l) != 11 {
		return ""
	}
	if !dddValido(l[:2]) {
		return ""
	}
	ass := l[2:]
	if len(ass) == 8 && ass[0] >= '6' {
		ass = "9" + ass // celular antigo, sem o nono digito
	}
	if len(ass) == 9 && ass[0] != '9' {
		return "" // 9 digitos so existe em celular, que comeca com 9
	}
	return "55" + l[:2] + ass
}

// Variantes devolve as chaves LOCAIS (formato de ChaveLocal) equivalentes ao
// telefone, para busca no banco: a com o nono digito e, para celular classico
// (9 + 6..9), a sem ele. Telefone que nao e BR plausivel devolve so a sua
// ChaveLocal (comportamento antigo). Vazio => nil.
func Variantes(raw string) []string {
	k := Chave(raw)
	if k == "" {
		if l := ChaveLocal(raw); l != "" {
			return []string{l}
		}
		return nil
	}
	com := k[2:]
	out := []string{com}
	if len(com) == 11 && com[2] == '9' && com[3] >= '6' {
		out = append(out, com[:2]+com[3:])
	}
	return out
}

// chavesPar devolve as Chaves de a e b; o lado sem DDD herda o DDD do outro.
func chavesPar(a, b string) (string, string) {
	ka, kb := Chave(a), Chave(b)
	if ka == "" && kb != "" {
		ka = ChaveComDDD(a, kb[2:4])
	}
	if kb == "" && ka != "" {
		kb = ChaveComDDD(b, ka[2:4])
	}
	return ka, kb
}

// Mesmo informa se a e b sao o MESMO telefone BR depois de padronizados (+55,
// DDD ausente de um lado, nono digito, espacos, parenteses, traco). Telefone
// invalido nunca e "o mesmo" de nada.
func Mesmo(a, b string) bool {
	ka, kb := chavesPar(a, b)
	return ka != "" && ka == kb
}

// Parecido informa se a e b, padronizados, diferem em NO MAXIMO 1 digito (um
// digito trocado, a mais ou a menos). Inclui o caso Mesmo. Telefone invalido
// nunca e parecido. Uso: aviso de possivel duplicado — NUNCA juntar sozinho.
func Parecido(a, b string) bool {
	ka, kb := chavesPar(a, b)
	if ka == "" || kb == "" {
		return false
	}
	return distanciaAteUm(ka, kb)
}

// distanciaAteUm: distancia de edicao (Levenshtein) <= 1, em O(n).
func distanciaAteUm(a, b string) bool {
	if a == b {
		return true
	}
	la, lb := len(a), len(b)
	if la-lb > 1 || lb-la > 1 {
		return false
	}
	i := 0
	for i < la && i < lb && a[i] == b[i] {
		i++
	}
	switch {
	case la == lb: // troca de 1 digito
		return a[i+1:] == b[i+1:]
	case la > lb: // a tem 1 a mais
		return a[i+1:] == b[i:]
	default: // b tem 1 a mais
		return a[i:] == b[i+1:]
	}
}
