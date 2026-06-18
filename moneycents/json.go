// Copyright 2026 imobo. Licença: privada.

package moneycents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// MarshalJSON serializa Cents como NUMBER decimal canonico ("1234.56").
//
// Decisao: serializa como numero (nao string) para manter compatibilidade com
// JavaScript/TypeScript do frontend. Como int64 max em centavos = ~92
// quatrilhoes de reais, o limite Number.MAX_SAFE_INTEGER (9.007 * 10^15)
// nao e atingido em valores monetarios reais.
//
// O ponto decimal e SEMPRE '.' no JSON (formato tecnico). A conversao para
// virgula brasileira e responsabilidade da camada de apresentacao.
func (c Cents) MarshalJSON() ([]byte, error) {
	return []byte(c.String()), nil
}

// UnmarshalJSON deserializa Cents aceitando 3 formatos:
//
//  1. Numero JSON: 1234.56 -> Cents(123456)
//  2. String com ponto: "1234.56" -> Cents(123456)
//  3. String com virgula (BR): "1234,56" ou "1.234,56" -> Cents(123456)
//
// Numeros muito grandes (> 9 quatrilhoes) podem perder precisao por causa do
// JSON parser do Go usar float64 internamente; nestes casos use string.
func (c *Cents) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return ErrEmptyString
	}
	// Trim espacos extremos.
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return ErrEmptyString
	}

	// Caso null: tratar como zero (decisao explicita).
	if bytes.Equal(data, []byte("null")) {
		*c = 0
		return nil
	}

	// String?
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("moneycents: nao foi possivel deserializar string JSON: %w", err)
		}
		v, err := FromString(s)
		if err != nil {
			return err
		}
		*c = v
		return nil
	}

	// Numero. Estrategia: ler como string raw e processar via FromString
	// para preservar precisao maxima quando possivel.
	raw := string(data)
	// Float JSON nao usa virgula, so ponto. Mas FromString tolera ambos.
	// Validar que nao tem caracteres invalidos.
	for _, r := range raw {
		if !(r == '-' || r == '+' || r == '.' || (r >= '0' && r <= '9') || r == 'e' || r == 'E') {
			return fmt.Errorf("%w: %q", ErrInvalidFormat, raw)
		}
	}

	// Caso tenha notacao cientifica (ex: "1e3"), delegar pro json padrao.
	if strings.ContainsAny(raw, "eE") {
		var f float64
		if err := json.Unmarshal(data, &f); err != nil {
			return fmt.Errorf("moneycents: notacao cientifica invalida: %w", err)
		}
		*c = FromReais(f)
		return nil
	}

	v, err := FromString(raw)
	if err != nil {
		return err
	}
	*c = v
	return nil
}
