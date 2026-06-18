// Copyright 2026 imobo. Licença: privada.

package moneycents

import (
	"database/sql/driver"
	"fmt"
)

// Scan implementa sql.Scanner para deserializar Cents a partir de uma coluna
// BIGINT (int64) do Postgres.
//
// Aceita os tipos comuns retornados pelos drivers Go:
//   - int64 (caso canonico)
//   - int32 / int (compatibilidade)
//   - []byte (string codificada — alguns drivers retornam BIGINT como bytes)
//   - string (raro, mas possivel)
//   - nil (popula como zero centavos)
//
// Outros tipos retornam erro.
func (c *Cents) Scan(src interface{}) error {
	if src == nil {
		*c = 0
		return nil
	}
	switch v := src.(type) {
	case int64:
		*c = Cents(v)
		return nil
	case int32:
		*c = Cents(v)
		return nil
	case int:
		*c = Cents(v)
		return nil
	case []byte:
		parsed, err := FromString(string(v))
		if err != nil {
			return fmt.Errorf("moneycents: Scan []byte: %w", err)
		}
		*c = parsed
		return nil
	case string:
		parsed, err := FromString(v)
		if err != nil {
			return fmt.Errorf("moneycents: Scan string: %w", err)
		}
		*c = parsed
		return nil
	default:
		return fmt.Errorf("moneycents: tipo nao suportado em Scan: %T", src)
	}
}

// Value implementa driver.Valuer para serializar Cents em coluna BIGINT do Postgres.
//
// Sempre retorna int64 (tipo canonico do BIGINT no driver pgx / lib/pq).
func (c Cents) Value() (driver.Value, error) {
	return int64(c), nil
}
