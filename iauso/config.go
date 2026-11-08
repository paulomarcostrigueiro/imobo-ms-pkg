// Copyright 2026 imobo. Licenca: privada.

package iauso

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/paulomarcostrigueiro/imobo-ms-pkg/claudeia"
)

// Modo e por onde a chamada passa. Ver doc.go.
type Modo string

const (
	ModoBiblioteca    Modo = "biblioteca"
	ModoMicrosservico Modo = "microsservico"
	ModoDesligado     Modo = "desligado"
	// ModoPlanoB so aparece no Registro: modo microsservico em que o ia-service
	// nao conectou e o servico chamou a Anthropic direto.
	ModoPlanoB Modo = "plano_b"
)

// OrganizacaoChaveTenant marca o uso pago com a chave PROPRIA do tenant
// (IAConfig). Entra no historico, mas nao conta no teto da IMOBO.
const OrganizacaoChaveTenant = "chave_tenant"

// Variaveis de ambiente. Os nomes sao os mesmos em todos os servicos.
const (
	EnvModo             = "IMOBO_IA_MODO"               // biblioteca | microsservico | desligado
	EnvURLMicrosservico = "IMOBO_IA_SERVICE_URL"        // ex.: http://ia-service:8080
	EnvOrganizacao      = "IMOBO_IA_ORGANIZACAO"        // organizacao da chave do servico: A, B...
	EnvLimites          = "IMOBO_IA_LIMITES"            // ver ParseLimites
	EnvConexaoMS        = "IMOBO_IA_SERVICE_CONEXAO_MS" // tempo para CONECTAR no ia-service antes do plano B
)

// Config e a configuracao de um servico.
type Config struct {
	// Servico e o nome curto de quem chama: "comunicacao", "fiscal"...
	Servico string
	// Modo e biblioteca (padrao), microsservico ou desligado.
	Modo Modo
	// URLMicrosservico e a base do ia-service (so no modo microsservico).
	URLMicrosservico string
	// TempoConexaoMicrosservico e quanto esperar para CONECTAR no ia-service.
	// Passou disso, plano B. Padrao 2 s.
	TempoConexaoMicrosservico time.Duration
	// Organizacao e a organizacao da Anthropic dona da chave do servico (A, B).
	Organizacao string
	// ChaveServico e a chave da IMOBO que o servico usa (env). Fica so em
	// memoria, como sha256: chamada com outra chave = chave do tenant.
	ChaveServico string
}

// ConfigDoEnv monta a Config lendo as variaveis IMOBO_IA_*. servico e
// chaveServico vem do proprio servico (ex.: "fiscal" e FISCAL_AI_API_KEY).
func ConfigDoEnv(servico, chaveServico string) (Config, error) {
	c := Config{
		Servico:          servico,
		Modo:             Modo(strings.ToLower(strings.TrimSpace(os.Getenv(EnvModo)))),
		URLMicrosservico: strings.TrimRight(strings.TrimSpace(os.Getenv(EnvURLMicrosservico)), "/"),
		Organizacao:      strings.TrimSpace(os.Getenv(EnvOrganizacao)),
		ChaveServico:     strings.TrimSpace(chaveServico),
	}
	if v := strings.TrimSpace(os.Getenv(EnvConexaoMS)); v != "" {
		ms, err := strconv.Atoi(v)
		if err != nil || ms <= 0 {
			return Config{}, fmt.Errorf("iauso: %s invalido: %q", EnvConexaoMS, v)
		}
		c.TempoConexaoMicrosservico = time.Duration(ms) * time.Millisecond
	}
	return c, c.validar()
}

func (c *Config) validar() error {
	if c.Modo == "" {
		c.Modo = ModoBiblioteca
	}
	if c.TempoConexaoMicrosservico <= 0 {
		c.TempoConexaoMicrosservico = 2 * time.Second
	}
	if strings.TrimSpace(c.Servico) == "" {
		return fmt.Errorf("iauso: Servico vazio")
	}
	switch c.Modo {
	case ModoBiblioteca, ModoDesligado:
	case ModoMicrosservico:
		if c.URLMicrosservico == "" {
			return fmt.Errorf("iauso: modo microsservico exige %s", EnvURLMicrosservico)
		}
	default:
		return fmt.Errorf("iauso: %s invalido: %q (use biblioteca, microsservico ou desligado)", EnvModo, c.Modo)
	}
	return nil
}

// impressao e o sha256 da chave. Vazio quando nao ha chave.
func impressao(chave string) string {
	chave = strings.TrimSpace(chave)
	if chave == "" {
		return ""
	}
	s := sha256.Sum256([]byte(chave))
	return fmt.Sprintf("%x", s[:])
}

// Limite e o que uma organizacao pode gastar em um ciclo da assinatura.
type Limite struct {
	// Credito e o credito do plano (US$ 200 no Ultra). Vence no fim do ciclo.
	Credito claudeia.MicroDolar
	// Teto e quanto pode passar do credito, pago no cartao. Atingiu = bloqueia.
	Teto claudeia.MicroDolar
	// DiaRenovacao e o dia do mes em que a assinatura renova (1 a 31).
	DiaRenovacao int
}

// Total e credito + teto: a partir dai a IA da organizacao para.
func (l Limite) Total() claudeia.MicroDolar { return l.Credito + l.Teto }

// ParseLimites le o formato de IMOBO_IA_LIMITES:
//
//	A:200:50:15,B:200:50:3
//
// organizacao:creditoUSD:tetoUSD:diaRenovacao, separados por virgula. Valores
// em dolar aceitam centavos com ponto ("2.50"). Texto vazio = sem limites
// (registra, mas nunca bloqueia).
func ParseLimites(s string) (map[string]Limite, error) {
	out := map[string]Limite{}
	s = strings.TrimSpace(s)
	if s == "" {
		return out, nil
	}
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		p := strings.Split(item, ":")
		if len(p) != 4 {
			return nil, fmt.Errorf("iauso: limite %q: use organizacao:credito:teto:dia", item)
		}
		org := strings.TrimSpace(p[0])
		if org == "" || org == OrganizacaoChaveTenant {
			return nil, fmt.Errorf("iauso: limite %q: organizacao invalida", item)
		}
		cred, err := ParseUSD(p[1])
		if err != nil {
			return nil, fmt.Errorf("iauso: limite %q: credito: %w", item, err)
		}
		teto, err := ParseUSD(p[2])
		if err != nil {
			return nil, fmt.Errorf("iauso: limite %q: teto: %w", item, err)
		}
		dia, err := strconv.Atoi(strings.TrimSpace(p[3]))
		if err != nil || dia < 1 || dia > 31 {
			return nil, fmt.Errorf("iauso: limite %q: dia de renovacao deve ser 1 a 31", item)
		}
		if _, dup := out[org]; dup {
			return nil, fmt.Errorf("iauso: organizacao %q repetida em %s", org, EnvLimites)
		}
		out[org] = Limite{Credito: cred, Teto: teto, DiaRenovacao: dia}
	}
	return out, nil
}

// ParseUSD converte "200", "2.5" ou "0.000001" em micro-dolares, sem float.
func ParseUSD(s string) (claudeia.MicroDolar, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "-") {
		return 0, fmt.Errorf("valor em dolar invalido: %q", s)
	}
	inteiro, frac, _ := strings.Cut(s, ".")
	if len(frac) > 6 {
		return 0, fmt.Errorf("valor em dolar com mais de 6 casas: %q", s)
	}
	i, err := strconv.ParseInt(inteiro, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("valor em dolar invalido: %q", s)
	}
	var f int64
	if frac != "" {
		f, err = strconv.ParseInt(frac+strings.Repeat("0", 6-len(frac)), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("valor em dolar invalido: %q", s)
		}
	}
	return claudeia.MicroDolar(i*1_000_000 + f), nil
}

// fusoBrasilia e UTC-3 fixo (o Brasil nao tem horario de verao desde 2019).
// Fixo de proposito: nao depende de tzdata na imagem.
var fusoBrasilia = time.FixedZone("BRT", -3*3600)

// InicioDoCiclo devolve quando comecou o ciclo da assinatura que contem agora,
// pelo dia de renovacao (meia-noite de Brasilia). Dia maior que o mes (31 em
// fevereiro) cai no ultimo dia do mes.
func InicioDoCiclo(agora time.Time, diaRenovacao int) time.Time {
	a := agora.In(fusoBrasilia)
	inicio := diaNoMes(a.Year(), a.Month(), diaRenovacao)
	if a.Before(inicio) {
		ano, mes := a.Year(), a.Month()-1
		if mes < time.January {
			ano, mes = ano-1, time.December
		}
		inicio = diaNoMes(ano, mes, diaRenovacao)
	}
	return inicio
}

func diaNoMes(ano int, mes time.Month, dia int) time.Time {
	ultimo := time.Date(ano, mes+1, 0, 0, 0, 0, 0, fusoBrasilia).Day()
	if dia > ultimo {
		dia = ultimo
	}
	return time.Date(ano, mes, dia, 0, 0, 0, 0, fusoBrasilia)
}
