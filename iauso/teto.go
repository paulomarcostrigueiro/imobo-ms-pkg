// Copyright 2026 imobo. Licenca: privada.

package iauso

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/paulomarcostrigueiro/imobo-ms-pkg/claudeia"
)

// FonteLimites diz o limite de cada organizacao. LimitesFixos le do env; o
// aumento pelo link do WhatsApp entra como outra FonteLimites, lida do banco.
type FonteLimites interface {
	Limite(ctx context.Context, organizacao string) (Limite, bool, error)
}

// LimitesFixos e a FonteLimites de IMOBO_IA_LIMITES.
type LimitesFixos map[string]Limite

func (l LimitesFixos) Limite(_ context.Context, org string) (Limite, bool, error) {
	v, ok := l[org]
	return v, ok, nil
}

// Nivel do aviso.
type Nivel int

const (
	Nivel80  Nivel = 80
	Nivel100 Nivel = 100
)

// Aviso e o que o Avisador recebe ao cruzar 80% e 100% do teto.
type Aviso struct {
	Organizacao string
	Nivel       Nivel
	// Excedente e quanto ja passou do credito no ciclo; Teto e o limite dele.
	Excedente   claudeia.MicroDolar
	Teto        claudeia.MicroDolar
	Credito     claudeia.MicroDolar
	InicioCiclo time.Time
}

// Avisador entrega o aviso (template de WhatsApp ao Paulo). Precisa ser
// idempotente por (organizacao, ciclo, nivel): o Teto so garante nao repetir
// dentro do mesmo processo.
type Avisador interface {
	Avisar(ctx context.Context, a Aviso) error
}

// AvisadorLog so escreve no log. Padrao ate o template do WhatsApp existir.
type AvisadorLog struct{ Log *slog.Logger }

func (a AvisadorLog) Avisar(_ context.Context, av Aviso) error {
	l := a.Log
	if l == nil {
		l = slog.Default()
	}
	l.Warn("iauso: teto de IA", "organizacao", av.Organizacao, "nivel", int(av.Nivel),
		"excedente", av.Excedente.String(), "teto", av.Teto.String())
	return nil
}

// Situacao e a foto do gasto de uma organizacao no ciclo.
type Situacao struct {
	TemLimite   bool
	Limite      Limite
	InicioCiclo time.Time
	Gasto       claudeia.MicroDolar
	// Bloqueado = Gasto >= credito + teto.
	Bloqueado bool
}

// Excedente e o que passou do credito (pago no cartao).
func (s Situacao) Excedente() claudeia.MicroDolar {
	if s.Gasto <= s.Limite.Credito {
		return 0
	}
	return s.Gasto - s.Limite.Credito
}

type gastoCache struct {
	ciclo  time.Time
	gasto  claudeia.MicroDolar
	lidoEm time.Time
}

// Teto confere o gasto contra o limite. O gasto vem do banco (GastoDesde) e e
// guardado por `validade`; entre uma leitura e outra, Somar acrescenta o custo
// de cada chamada, para o bloqueio nao esperar a proxima leitura.
//
// Banco fora do ar e sem leitura anterior: NAO bloqueia (a IA nao para por
// falha do controle). A trava final e o teto nativo do console da Anthropic.
type Teto struct {
	limites  FonteLimites
	gasto    Gravador
	avisador Avisador
	log      *slog.Logger

	validade time.Duration
	agora    func() time.Time

	mu       sync.Mutex
	cache    map[string]gastoCache
	avisados map[string]bool
}

// NovoTeto cria o controle. avisador nil = AvisadorLog.
func NovoTeto(limites FonteLimites, gasto Gravador, avisador Avisador, log *slog.Logger) *Teto {
	if log == nil {
		log = slog.Default()
	}
	if avisador == nil {
		avisador = AvisadorLog{Log: log}
	}
	return &Teto{
		limites: limites, gasto: gasto, avisador: avisador, log: log,
		validade: 30 * time.Second,
		agora:    time.Now,
		cache:    map[string]gastoCache{},
		avisados: map[string]bool{},
	}
}

// Verificar devolve a situacao da organizacao agora.
func (t *Teto) Verificar(ctx context.Context, org string) Situacao {
	lim, ok, err := t.limites.Limite(ctx, org)
	if err != nil {
		t.log.Warn("iauso: limite indisponivel; sem bloqueio", "organizacao", org, "erro", err)
		return Situacao{}
	}
	if !ok {
		return Situacao{}
	}
	agora := t.agora()
	ciclo := InicioDoCiclo(agora, lim.DiaRenovacao)
	s := Situacao{TemLimite: true, Limite: lim, InicioCiclo: ciclo}

	t.mu.Lock()
	c, tem := t.cache[org]
	t.mu.Unlock()
	if !tem || !c.ciclo.Equal(ciclo) || agora.Sub(c.lidoEm) >= t.validade {
		g, err := t.gasto.GastoDesde(ctx, org, ciclo)
		if err != nil {
			t.log.Warn("iauso: gasto indisponivel; usando o ultimo lido", "organizacao", org, "erro", err)
			if !tem || !c.ciclo.Equal(ciclo) {
				return s // sem dado nenhum do ciclo: nao bloqueia
			}
		} else {
			c = gastoCache{ciclo: ciclo, gasto: g, lidoEm: agora}
			t.mu.Lock()
			t.cache[org] = c
			t.mu.Unlock()
		}
	}
	s.Gasto = c.gasto
	s.Bloqueado = lim.Total() > 0 && s.Gasto >= lim.Total()
	t.talvezAvisar(ctx, org, s)
	return s
}

// Somar acrescenta o custo de uma chamada ao gasto em memoria.
func (t *Teto) Somar(ctx context.Context, org string, custo claudeia.MicroDolar) {
	if custo <= 0 {
		return
	}
	t.mu.Lock()
	c, tem := t.cache[org]
	if tem {
		c.gasto += custo
		t.cache[org] = c
	}
	t.mu.Unlock()
	if tem {
		t.Verificar(ctx, org) // so para disparar aviso ao cruzar 80%/100%
	}
}

func (t *Teto) talvezAvisar(ctx context.Context, org string, s Situacao) {
	if s.Limite.Teto <= 0 {
		return
	}
	exc := s.Excedente()
	var nivel Nivel
	switch {
	case exc >= s.Limite.Teto:
		nivel = Nivel100
	case exc*100 >= s.Limite.Teto*80:
		nivel = Nivel80
	default:
		return
	}
	chave := org + "|" + s.InicioCiclo.Format(time.DateOnly) + "|" + strconv.Itoa(int(nivel))
	t.mu.Lock()
	ja := t.avisados[chave]
	t.avisados[chave] = true
	t.mu.Unlock()
	if ja {
		return
	}
	av := Aviso{Organizacao: org, Nivel: nivel, Excedente: exc, Teto: s.Limite.Teto, Credito: s.Limite.Credito, InicioCiclo: s.InicioCiclo}
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := t.avisador.Avisar(ctx, av); err != nil {
			t.log.Error("iauso: aviso de teto falhou", "organizacao", org, "nivel", int(nivel), "erro", err)
		}
	}()
}
