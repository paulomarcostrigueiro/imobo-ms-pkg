// Copyright 2026 imobo. Licenca: privada.

package iauso

import (
	"regexp"

	"github.com/paulomarcostrigueiro/imobo-ms-pkg/claudeia"
)

// usoMedido e o `usage` da Messages API, com o detalhe do cache.
type usoMedido struct {
	Entrada      int
	Saida        int
	CacheLeitura int
	// CacheEscrita5m e CacheEscrita1h somam cache_creation_input_tokens.
	CacheEscrita5m int
	CacheEscrita1h int
}

func (u usoMedido) cacheEscrita() int { return u.CacheEscrita5m + u.CacheEscrita1h }

// precoMaisCaro e usado quando o modelo nao esta na tabela: o teto erra para
// mais, nunca para menos.
var precoMaisCaro = claudeia.Preco{Entrada: 10, Saida: 50}

var sufixoData = regexp.MustCompile(`-\d{8}$`)

// precoDoModelo procura o id exato e, se nao achar, sem o sufixo de data
// ("claude-haiku-4-5-20251001" -> "claude-haiku-4-5").
func precoDoModelo(modelo string) (claudeia.Preco, bool) {
	if p, err := claudeia.PrecoDe(modelo); err == nil {
		return p, true
	}
	if base := sufixoData.ReplaceAllString(modelo, ""); base != modelo {
		if p, err := claudeia.PrecoDe(base); err == nil {
			return p, true
		}
	}
	return precoMaisCaro, false
}

// custoDe calcula com os multiplicadores oficiais do cache de prompt:
// escrita 5 min = 1,25x a entrada; escrita 1 h = 2x; leitura = 0,1x.
// A conta e em centesimos de micro-dolar e arredonda para CIMA no fim.
func custoDe(p claudeia.Preco, u usoMedido) claudeia.MicroDolar {
	e, s := int64(p.Entrada), int64(p.Saida)
	c := int64(u.Entrada)*e*100 +
		int64(u.Saida)*s*100 +
		int64(u.CacheEscrita5m)*e*125 +
		int64(u.CacheEscrita1h)*e*200 +
		int64(u.CacheLeitura)*e*10
	m := c / 100
	if c%100 != 0 {
		m++
	}
	return claudeia.MicroDolar(m)
}
