// Copyright 2026 imobo. Licenca: privada.

package iauso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/paulomarcostrigueiro/imobo-ms-pkg/claudeia"
)

// Status de uma chamada no historico.
type Status string

const (
	StatusOK           Status = "ok"
	StatusErro         Status = "erro"          // a Anthropic respondeu erro (4xx/5xx)
	StatusSemCredito   Status = "sem_credito"   // "credit balance is too low"
	StatusTetoAtingido Status = "teto_atingido" // bloqueado pelo teto da IMOBO, nao chamou a Anthropic
	StatusFalhaRede    Status = "falha_rede"    // nao chegou resposta
)

// Registro e UMA chamada a Anthropic. So numeros e identificadores: nunca o
// texto das mensagens nem a chave (LGPD, LEI-MS #11).
type Registro struct {
	ID                 uuid.UUID
	CriadoEm           time.Time
	TenantID           *uuid.UUID
	Servico            string
	Funcionalidade     string
	Organizacao        string
	Modo               Modo
	Modelo             string
	TokensEntrada      int
	TokensSaida        int
	TokensCacheEscrita int
	TokensCacheLeitura int
	Custo              claudeia.MicroDolar
	// PrecoConhecido e falso quando o modelo nao esta na tabela do claudeia.
	// Nesse caso Custo usa o preco do modelo mais caro (para o teto errar para
	// mais, nunca para menos).
	PrecoConhecido bool
	RequestID      string
	Status         Status
	HTTPStatus     int
	DuracaoMS      int64
	ConversaID     string

	// cache1h e a parte de TokensCacheEscrita com cache de 1 hora (custa 2x,
	// contra 1,25x do de 5 min). So entra na conta do custo; nao e gravado.
	cache1h int
	// estimado: resposta 200 sem usage; tokens estimados por cima (custo pelo
	// modelo mais caro, PrecoConhecido = false).
	estimado bool
}

// ErrRegistrosInvalidos: o banco recusou esses registros pelo CONTEUDO (valor
// fora do formato), nao por estar fora do ar. Repetir nao adianta: a Fila os
// escreve no log e segue com o resto, que ja foi gravado.
type ErrRegistrosInvalidos struct {
	Indices []int
	Causa   error
}

func (e *ErrRegistrosInvalidos) Error() string {
	return fmt.Sprintf("iauso: %d registro(s) invalido(s): %v", len(e.Indices), e.Causa)
}

func (e *ErrRegistrosInvalidos) Unwrap() error { return e.Causa }

// Gravador persiste registros e soma o gasto. PostgresGravador e o real.
// Gravar devolve *ErrRegistrosInvalidos quando so alguns registros sao ruins.
type Gravador interface {
	Gravar(ctx context.Context, rs []Registro) error
	GastoDesde(ctx context.Context, organizacao string, desde time.Time) (claudeia.MicroDolar, error)
}

// Fila grava em segundo plano, para a chamada a IA nunca esperar o banco.
//
// Se o banco falhar, os registros ficam pendentes em memoria e sao tentados de
// novo. Acima de maxPendentes, o mais antigo sai da fila e e escrito no log como
// JSON ("iauso: registro descartado"), que e o caminho para recupera-lo. Um
// restart com pendentes tambem os escreve no log em Fechar.
type Fila struct {
	g   Gravador
	log *slog.Logger
	ch  chan Registro

	maxPendentes int
	lote         int
	intervalo    time.Duration

	fechar  chan struct{}
	fechada atomic.Bool
	wg      sync.WaitGroup
	once    sync.Once
}

// NovaFila cria e INICIA a fila. Chame Fechar no shutdown.
func NovaFila(g Gravador, log *slog.Logger) *Fila {
	if log == nil {
		log = slog.Default()
	}
	f := &Fila{
		g: g, log: log,
		ch:           make(chan Registro, 1000),
		maxPendentes: 10_000,
		lote:         100,
		intervalo:    time.Second,
		fechar:       make(chan struct{}),
	}
	f.wg.Add(1)
	go f.rodar()
	return f
}

// Enviar entrega um registro sem bloquear. Fila cheia ou ja fechada = log e
// segue.
func (f *Fila) Enviar(r Registro) {
	if f.fechada.Load() {
		f.descartar(r, "fila ja encerrada")
		return
	}
	select {
	case f.ch <- r:
	default:
		f.descartar(r, "fila cheia")
	}
}

// Fechar grava o que der ate ctx vencer; o resto vai para o log.
//
// Use um ctx PROPRIO (nao o do Shutdown do HTTP, que pode ja ter vencido).
func (f *Fila) Fechar(ctx context.Context) {
	f.once.Do(func() { f.fechada.Store(true); close(f.fechar) })
	done := make(chan struct{})
	go func() { f.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (f *Fila) rodar() {
	defer f.wg.Done()
	var pend []Registro
	novos := 0 // chegados desde a ultima tentativa
	espera := f.intervalo
	t := time.NewTimer(espera)
	defer t.Stop()
	for {
		select {
		case r := <-f.ch:
			pend = append(pend, r)
			novos++
			// Lote cheio grava na hora, MAS nao durante o backoff (banco fora):
			// ai so o timer tenta, para nao martelar o banco a cada registro.
			if novos < f.lote || espera > f.intervalo {
				continue
			}
		case <-t.C:
		case <-f.fechar:
			for {
				select {
				case r := <-f.ch:
					pend = append(pend, r)
					continue
				default:
				}
				break
			}
			// ate 3 tentativas: o banco pode estar voltando junto com o deploy
			for i := 0; i < 3 && len(pend) > 0; i++ {
				if i > 0 {
					time.Sleep(200 * time.Millisecond)
				}
				pend = f.gravar(pend)
			}
			for _, r := range pend {
				f.descartar(r, "encerrando com o banco indisponivel")
			}
			return
		}
		novos = 0
		antes := len(pend)
		pend = f.gravar(pend)
		if len(pend) > 0 && len(pend) == antes {
			if espera < time.Minute {
				espera *= 2
			}
		} else {
			espera = f.intervalo
		}
		for len(pend) > f.maxPendentes {
			f.descartar(pend[0], "pendentes demais")
			pend = pend[1:]
		}
		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
		t.Reset(espera)
	}
}

// gravar tenta em lotes; devolve o que nao entrou.
func (f *Fila) gravar(pend []Registro) []Registro {
	for len(pend) > 0 {
		n := min(len(pend), f.lote)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := f.g.Gravar(ctx, pend[:n])
		cancel()
		var inv *ErrRegistrosInvalidos
		if errors.As(err, &inv) {
			for _, i := range inv.Indices {
				if i >= 0 && i < n {
					f.descartar(pend[i], "recusado pelo banco: "+inv.Causa.Error())
				}
			}
			pend = pend[n:]
			continue
		}
		if err != nil {
			f.log.Warn("iauso: banco indisponivel; registros pendentes", "pendentes", len(pend), "erro", err)
			return pend
		}
		pend = pend[n:]
	}
	return pend[:0]
}

func (f *Fila) descartar(r Registro, motivo string) {
	b, _ := json.Marshal(r)
	f.log.Error("iauso: registro descartado", "motivo", motivo, "registro", string(b))
}

// sanear corta cada texto ao tamanho da coluna, tira \x00 (o jsonb recusa) e
// zera numeros negativos. Um registro estranho nunca pode travar a fila.
func sanear(r Registro) Registro {
	r.Servico = cortar(r.Servico, 40)
	r.Funcionalidade = cortar(r.Funcionalidade, 60)
	r.Organizacao = cortar(r.Organizacao, 40)
	r.Modelo = cortar(r.Modelo, 80)
	r.RequestID = cortar(r.RequestID, 120)
	r.ConversaID = cortar(r.ConversaID, 80)
	for _, p := range []*int{&r.TokensEntrada, &r.TokensSaida, &r.TokensCacheEscrita, &r.TokensCacheLeitura, &r.HTTPStatus} {
		if *p < 0 {
			*p = 0
		}
	}
	if r.Custo < 0 {
		r.Custo = 0
	}
	if r.DuracaoMS < 0 {
		r.DuracaoMS = 0
	}
	return r
}

func cortar(s string, max int) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	s = strings.ReplaceAll(s, "\x00", "")
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}
