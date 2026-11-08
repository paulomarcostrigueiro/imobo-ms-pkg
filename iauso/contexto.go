// Copyright 2026 imobo. Licenca: privada.

package iauso

import (
	"context"

	"github.com/google/uuid"

	"github.com/paulomarcostrigueiro/imobo-ms-pkg/tenantctx"
)

// FuncionalidadeNaoInformada e o rotulo de quem chamou sem ComFuncionalidade.
const FuncionalidadeNaoInformada = "nao_informada"

type chaveFuncionalidade struct{}
type chaveTenant struct{}
type chaveConversa struct{}
type chaveOrigem struct{}

// ComFuncionalidade marca a chamada com o nome da funcionalidade que o usuario
// ve ("chat_atendimento", "copiloto", "importacao_colunas"...). E o que aparece
// no card de gasto. Use nomes curtos, fixos, em snake_case.
func ComFuncionalidade(ctx context.Context, nome string) context.Context {
	return context.WithValue(ctx, chaveFuncionalidade{}, nome)
}

// ComTenant marca o tenant quando o ctx nao tem tenantctx (jobs, webhooks).
func ComTenant(ctx context.Context, tenantID uuid.UUID) context.Context {
	return context.WithValue(ctx, chaveTenant{}, tenantID)
}

// ComConversa marca a conversa do atendimento (opcional).
func ComConversa(ctx context.Context, conversaID string) context.Context {
	return context.WithValue(ctx, chaveConversa{}, conversaID)
}

// origem e de onde veio uma chamada. Montada a partir do ctx (servico) ou dos
// cabecalhos X-Imobo-* (ia-service).
type origem struct {
	Servico        string
	Funcionalidade string
	TenantID       *uuid.UUID
	ConversaID     string
	Organizacao    string
	// Modo, quando preenchido, sobrepoe o modo do Transporte no registro (o
	// ia-service grava "microsservico" usando o Transporte em modo biblioteca).
	Modo Modo
}

func comOrigem(ctx context.Context, o origem) context.Context {
	return context.WithValue(ctx, chaveOrigem{}, o)
}

func origemDe(ctx context.Context) (origem, bool) {
	o, ok := ctx.Value(chaveOrigem{}).(origem)
	return o, ok
}

func funcionalidadeDe(ctx context.Context) string {
	if v, ok := ctx.Value(chaveFuncionalidade{}).(string); ok && v != "" {
		return v
	}
	return FuncionalidadeNaoInformada
}

func tenantDe(ctx context.Context) *uuid.UUID {
	if v, ok := ctx.Value(chaveTenant{}).(uuid.UUID); ok && v != uuid.Nil {
		return &v
	}
	if tc, ok := tenantctx.From(ctx); ok && tc.ActedAsTenantID != uuid.Nil {
		id := tc.ActedAsTenantID
		return &id
	}
	return nil
}

func conversaDe(ctx context.Context) string {
	v, _ := ctx.Value(chaveConversa{}).(string)
	return v
}
