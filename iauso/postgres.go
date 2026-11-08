// Copyright 2026 imobo. Licenca: privada.

package iauso

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/paulomarcostrigueiro/imobo-ms-pkg/claudeia"
)

// DB e o minimo de *pgxpool.Pool que o gravador usa.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

var schemaValido = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// PostgresGravador grava em <schema>.ia_uso, a tabela criada pela migration de
// cada servico (modelo em SQLTabela).
type PostgresGravador struct {
	db     DB
	tabela string
}

// NovoPostgresGravador usa o schema do servico ("comunicacao", "fiscal", "ia").
func NovoPostgresGravador(db DB, schema string) (*PostgresGravador, error) {
	if !schemaValido.MatchString(schema) {
		return nil, fmt.Errorf("iauso: schema invalido: %q", schema)
	}
	return &PostgresGravador{db: db, tabela: schema + ".ia_uso"}, nil
}

// Gravar insere em lote. ON CONFLICT (id) DO NOTHING: repetir apos falha nao
// duplica.
func (g *PostgresGravador) Gravar(ctx context.Context, rs []Registro) error {
	if len(rs) == 0 {
		return nil
	}
	sql := `INSERT INTO ` + g.tabela + ` (
		id, criado_em, tenant_id, servico, funcionalidade, organizacao, modo, modelo,
		tokens_entrada, tokens_saida, tokens_cache_escrita, tokens_cache_leitura,
		custo_micro_usd, preco_conhecido, request_id_anthropic, status, http_status,
		duracao_ms, conversa_id
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
	ON CONFLICT (id) DO NOTHING`
	b := &pgx.Batch{}
	for _, r := range rs {
		b.Queue(sql,
			r.ID, r.CriadoEm, r.TenantID, r.Servico, r.Funcionalidade, r.Organizacao, string(r.Modo), r.Modelo,
			r.TokensEntrada, r.TokensSaida, r.TokensCacheEscrita, r.TokensCacheLeitura,
			int64(r.Custo), r.PrecoConhecido, nulo(r.RequestID), string(r.Status), r.HTTPStatus,
			r.DuracaoMS, nulo(r.ConversaID),
		)
	}
	br := g.db.SendBatch(ctx, b)
	for range rs {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return fmt.Errorf("iauso: gravar: %w", err)
		}
	}
	return br.Close()
}

// GastoDesde soma o custo da organizacao a partir de um instante.
func (g *PostgresGravador) GastoDesde(ctx context.Context, organizacao string, desde time.Time) (claudeia.MicroDolar, error) {
	var total int64
	err := g.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(custo_micro_usd), 0)::bigint FROM `+g.tabela+` WHERE organizacao = $1 AND criado_em >= $2`,
		organizacao, desde).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("iauso: gasto: %w", err)
	}
	return claudeia.MicroDolar(total), nil
}

func nulo(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// SQLTabela devolve o SQL da tabela para a migration do servico. Copie o
// resultado para a migration (o runner de cada servico nao aceita codigo Go);
// o teste do pacote garante que este texto e o que o gravador espera.
//
// appRole e o papel da aplicacao (ex.: "fiscal_app"), que so ganha SELECT e
// INSERT: o historico nunca e alterado nem apagado pela aplicacao.
func SQLTabela(schema, appRole string) string {
	r := strings.NewReplacer("{schema}", schema, "{app}", appRole)
	return r.Replace(`CREATE TABLE IF NOT EXISTS {schema}.ia_uso (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    criado_em             TIMESTAMPTZ NOT NULL DEFAULT now(),
    tenant_id             UUID,
    servico               VARCHAR(40)  NOT NULL,
    funcionalidade        VARCHAR(60)  NOT NULL,
    organizacao           VARCHAR(40)  NOT NULL,
    modo                  VARCHAR(20)  NOT NULL,
    modelo                VARCHAR(80)  NOT NULL DEFAULT '',
    tokens_entrada        INTEGER      NOT NULL DEFAULT 0,
    tokens_saida          INTEGER      NOT NULL DEFAULT 0,
    tokens_cache_escrita  INTEGER      NOT NULL DEFAULT 0,
    tokens_cache_leitura  INTEGER      NOT NULL DEFAULT 0,
    custo_micro_usd       BIGINT       NOT NULL DEFAULT 0,
    preco_conhecido       BOOLEAN      NOT NULL DEFAULT true,
    request_id_anthropic  VARCHAR(120),
    status                VARCHAR(20)  NOT NULL,
    http_status           INTEGER      NOT NULL DEFAULT 0,
    duracao_ms            BIGINT       NOT NULL DEFAULT 0,
    conversa_id           VARCHAR(80),
    CONSTRAINT ia_uso_modo_chk   CHECK (modo IN ('biblioteca', 'microsservico', 'plano_b')),
    CONSTRAINT ia_uso_status_chk CHECK (status IN ('ok', 'erro', 'sem_credito', 'teto_atingido', 'falha_rede'))
);

CREATE INDEX IF NOT EXISTS ia_uso_org_criado_idx    ON {schema}.ia_uso (organizacao, criado_em);
CREATE INDEX IF NOT EXISTS ia_uso_tenant_criado_idx ON {schema}.ia_uso (tenant_id, criado_em);

GRANT SELECT, INSERT ON {schema}.ia_uso TO {app};
`)
}
