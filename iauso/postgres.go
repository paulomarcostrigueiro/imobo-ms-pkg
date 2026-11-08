// Copyright 2026 imobo. Licenca: privada.

package iauso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/paulomarcostrigueiro/imobo-ms-pkg/claudeia"
)

// DB e o minimo de *pgxpool.Pool que o gravador usa.
type DB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var schemaValido = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// PostgresGravador grava em <schema>.ia_uso pelas funcoes da migration de cada
// servico (modelo em SQLTabela). A aplicacao nao tem INSERT direto na tabela.
type PostgresGravador struct {
	db     DB
	schema string
}

// NovoPostgresGravador usa o schema do servico ("comunicacao", "fiscal", "ia").
func NovoPostgresGravador(db DB, schema string) (*PostgresGravador, error) {
	if !schemaValido.MatchString(schema) {
		return nil, fmt.Errorf("iauso: schema invalido: %q", schema)
	}
	return &PostgresGravador{db: db, schema: schema}, nil
}

// linha e o Registro com os nomes das colunas (jsonb_populate_recordset).
type linha struct {
	ID                 uuid.UUID  `json:"id"`
	CriadoEm           time.Time  `json:"criado_em"`
	TenantID           *uuid.UUID `json:"tenant_id"`
	Servico            string     `json:"servico"`
	Funcionalidade     string     `json:"funcionalidade"`
	Organizacao        string     `json:"organizacao"`
	Modo               string     `json:"modo"`
	Modelo             string     `json:"modelo"`
	TokensEntrada      int        `json:"tokens_entrada"`
	TokensSaida        int        `json:"tokens_saida"`
	TokensCacheEscrita int        `json:"tokens_cache_escrita"`
	TokensCacheLeitura int        `json:"tokens_cache_leitura"`
	Custo              int64      `json:"custo_micro_usd"`
	PrecoConhecido     bool       `json:"preco_conhecido"`
	RequestID          *string    `json:"request_id_anthropic"`
	Status             string     `json:"status"`
	HTTPStatus         int        `json:"http_status"`
	DuracaoMS          int64      `json:"duracao_ms"`
	ConversaID         *string    `json:"conversa_id"`
}

// Gravar manda o lote inteiro numa chamada a <schema>.ia_registrar. Repetir
// apos falha nao duplica (ON CONFLICT (id) DO NOTHING dentro da funcao).
func (g *PostgresGravador) Gravar(ctx context.Context, rs []Registro) error {
	if len(rs) == 0 {
		return nil
	}
	err := g.gravarLote(ctx, rs)
	if err == nil || !ehErroDeDado(err) || len(rs) == 1 {
		if err != nil && ehErroDeDado(err) {
			return &ErrRegistrosInvalidos{Indices: []int{0}, Causa: err}
		}
		return err
	}
	// O lote foi recusado pelo conteudo de algum registro: grava um a um e
	// separa so os ruins. Os que ja entraram nao duplicam (ON CONFLICT).
	inv := &ErrRegistrosInvalidos{}
	for i := range rs {
		if err := g.gravarLote(ctx, rs[i:i+1]); err != nil {
			if !ehErroDeDado(err) {
				return err // banco caiu no meio: a fila repete o lote inteiro
			}
			inv.Indices = append(inv.Indices, i)
			inv.Causa = err
		}
	}
	if len(inv.Indices) > 0 {
		return inv
	}
	return nil
}

// ehErroDeDado: classe 22 (dado invalido) ou 23 (restricao violada).
func ehErroDeDado(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && (strings.HasPrefix(pg.Code, "22") || strings.HasPrefix(pg.Code, "23"))
}

func (g *PostgresGravador) gravarLote(ctx context.Context, rs []Registro) error {
	ls := make([]linha, len(rs))
	for i, r := range rs {
		r = sanear(r)
		ls[i] = linha{
			ID: r.ID, CriadoEm: r.CriadoEm, TenantID: r.TenantID, Servico: r.Servico,
			Funcionalidade: r.Funcionalidade, Organizacao: r.Organizacao, Modo: string(r.Modo), Modelo: r.Modelo,
			TokensEntrada: r.TokensEntrada, TokensSaida: r.TokensSaida,
			TokensCacheEscrita: r.TokensCacheEscrita, TokensCacheLeitura: r.TokensCacheLeitura,
			Custo: int64(r.Custo), PrecoConhecido: r.PrecoConhecido, RequestID: nulo(r.RequestID),
			Status: string(r.Status), HTTPStatus: r.HTTPStatus, DuracaoMS: r.DuracaoMS, ConversaID: nulo(r.ConversaID),
		}
	}
	b, err := json.Marshal(ls)
	if err != nil {
		return fmt.Errorf("iauso: gravar: %w", err)
	}
	var n int
	if err := g.db.QueryRow(ctx, `SELECT `+g.schema+`.ia_registrar($1::jsonb)`, string(b)).Scan(&n); err != nil {
		return fmt.Errorf("iauso: gravar: %w", err)
	}
	return nil
}

// GastoDesde soma o custo da organizacao a partir de um instante.
func (g *PostgresGravador) GastoDesde(ctx context.Context, organizacao string, desde time.Time) (claudeia.MicroDolar, error) {
	var total int64
	err := g.db.QueryRow(ctx, `SELECT `+g.schema+`.ia_gasto_desde($1, $2)`, organizacao, desde).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("iauso: gasto: %w", err)
	}
	return claudeia.MicroDolar(total), nil
}

func nulo(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// SQLTabela devolve o SQL da tabela para a migration do servico. Copie o
// resultado para a migration (o runner de cada servico nao aceita codigo Go);
// o teste do pacote garante que este texto e o que o gravador espera.
//
// appRole e o papel da aplicacao (ex.: "fiscal_app"), que so ganha SELECT na
// tabela e EXECUTE nas duas funcoes: o historico nunca e alterado nem apagado
// pela aplicacao.
func SQLTabela(schema, appRole string) string {
	if !schemaValido.MatchString(schema) || !schemaValido.MatchString(appRole) {
		panic(fmt.Sprintf("iauso: SQLTabela com schema/role invalido: %q %q", schema, appRole))
	}
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
    CONSTRAINT ia_uso_status_chk CHECK (status IN ('ok', 'erro', 'sem_credito', 'teto_atingido', 'falha_rede')),
    CONSTRAINT ia_uso_numeros_chk CHECK (
        tokens_entrada >= 0 AND tokens_saida >= 0 AND tokens_cache_escrita >= 0
        AND tokens_cache_leitura >= 0 AND custo_micro_usd >= 0 AND duracao_ms >= 0)
);

CREATE INDEX IF NOT EXISTS ia_uso_org_criado_idx    ON {schema}.ia_uso (organizacao, criado_em);
CREATE INDEX IF NOT EXISTS ia_uso_tenant_criado_idx ON {schema}.ia_uso (tenant_id, criado_em);

-- RLS (LEI-MS #30). LER: so o proprio tenant, ou o master. GRAVAR: so pela
-- funcao ia_registrar (a fila grava em segundo plano, sem tenant no contexto;
-- o registro e de contabilidade, sem conteudo de mensagem).
ALTER TABLE {schema}.ia_uso ENABLE ROW LEVEL SECURITY;
ALTER TABLE {schema}.ia_uso FORCE  ROW LEVEL SECURITY;
DROP POLICY IF EXISTS ia_uso_ler ON {schema}.ia_uso;
CREATE POLICY ia_uso_ler ON {schema}.ia_uso FOR SELECT
    USING (
        tenant_id = ANY(NULLIF(current_setting('app.tenant_ids_visiveis', true), '')::uuid[])
        OR COALESCE(NULLIF(current_setting('app.is_master_imobo', true), ''), 'false')::boolean
    );
DROP POLICY IF EXISTS ia_uso_gravar ON {schema}.ia_uso;
CREATE POLICY ia_uso_gravar ON {schema}.ia_uso FOR INSERT WITH CHECK (true);

-- A aplicacao so LE a tabela; grava pela funcao. O historico nunca e alterado
-- nem apagado. O REVOKE desfaz o INSERT/UPDATE/DELETE que o ALTER DEFAULT
-- PRIVILEGES do servico da a toda tabela nova.
REVOKE ALL ON {schema}.ia_uso FROM {app};
GRANT SELECT ON {schema}.ia_uso TO {app};

-- Grava um lote (array JSON com os nomes das colunas). ON CONFLICT (id) DO
-- NOTHING: a fila pode reenviar um lote sem duplicar. Liga a visao master so
-- durante o INSERT porque, com ON CONFLICT, o Postgres confere a linha que ja
-- existe contra a politica de LEITURA.
CREATE OR REPLACE FUNCTION {schema}.ia_registrar(p_registros JSONB)
RETURNS INTEGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, {schema}
AS $$
DECLARE
    v_antes TEXT := current_setting('app.is_master_imobo', true);
    v_n     INTEGER;
BEGIN
    PERFORM set_config('app.is_master_imobo', 'true', true);
    INSERT INTO {schema}.ia_uso (
        id, criado_em, tenant_id, servico, funcionalidade, organizacao, modo, modelo,
        tokens_entrada, tokens_saida, tokens_cache_escrita, tokens_cache_leitura,
        custo_micro_usd, preco_conhecido, request_id_anthropic, status, http_status,
        duracao_ms, conversa_id)
    SELECT
        id, criado_em, tenant_id, servico, funcionalidade, organizacao, modo, modelo,
        tokens_entrada, tokens_saida, tokens_cache_escrita, tokens_cache_leitura,
        custo_micro_usd, preco_conhecido, request_id_anthropic, status, http_status,
        duracao_ms, conversa_id
      FROM jsonb_populate_recordset(NULL::{schema}.ia_uso, p_registros)
    ON CONFLICT (id) DO NOTHING;
    GET DIAGNOSTICS v_n = ROW_COUNT;
    PERFORM set_config('app.is_master_imobo', COALESCE(v_antes, ''), true);
    RETURN v_n;
END
$$;
REVOKE ALL ON FUNCTION {schema}.ia_registrar(JSONB) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION {schema}.ia_registrar(JSONB) TO {app};

-- Gasto da organizacao no ciclo, somando TODOS os tenants (o teto e por
-- organizacao). Devolve so um numero. Com FORCE RLS nem o dono passa pela
-- politica; por isso a funcao liga a visao master SO durante a soma e devolve
-- o valor anterior antes de sair (nao vaza para o resto da transacao). Um
-- "SET app.x" na definicao da funcao exigiria superusuario.
CREATE OR REPLACE FUNCTION {schema}.ia_gasto_desde(p_organizacao TEXT, p_desde TIMESTAMPTZ)
RETURNS BIGINT
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, {schema}
AS $$
DECLARE
    v_antes TEXT := current_setting('app.is_master_imobo', true);
    v_total BIGINT;
BEGIN
    PERFORM set_config('app.is_master_imobo', 'true', true);
    SELECT COALESCE(SUM(custo_micro_usd), 0)::bigint INTO v_total
      FROM {schema}.ia_uso
     WHERE organizacao = p_organizacao AND criado_em >= p_desde;
    PERFORM set_config('app.is_master_imobo', COALESCE(v_antes, ''), true);
    RETURN v_total;
END
$$;
REVOKE ALL ON FUNCTION {schema}.ia_gasto_desde(TEXT, TIMESTAMPTZ) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION {schema}.ia_gasto_desde(TEXT, TIMESTAMPTZ) TO {app};
`)
}
