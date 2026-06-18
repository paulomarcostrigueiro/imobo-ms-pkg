// Copyright 2026 imobo. Licença: privada.

package moneycents

import (
	"math/rand"
	"testing"
	"testing/quick"
)

// TestProperty_AddSubIdentity verifica c.Add(c2).Sub(c2) == c.
//
// Restringe valores para evitar overflow: faixa segura de [-10^15, 10^15] centavos
// (ate ~10 trilhoes de reais, mais que suficiente para casos reais).
func TestProperty_AddSubIdentity(t *testing.T) {
	prop := func(a, b int64) bool {
		ca := safeRange(a)
		cb := safeRange(b)
		return ca.Add(cb).Sub(cb) == ca
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 10000}); err != nil {
		t.Errorf("propriedade aditiva falhou: %v", err)
	}
}

// TestProperty_Commutativity verifica c1.Add(c2) == c2.Add(c1).
func TestProperty_Commutativity(t *testing.T) {
	prop := func(a, b int64) bool {
		ca := safeRange(a)
		cb := safeRange(b)
		return ca.Add(cb) == cb.Add(ca)
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 10000}); err != nil {
		t.Errorf("comutatividade falhou: %v", err)
	}
}

// TestProperty_Associativity verifica (a+b)+c == a+(b+c).
func TestProperty_Associativity(t *testing.T) {
	prop := func(a, b, c int64) bool {
		ca := safeRange(a / 3) // dividir por 3 para garantir que soma nao overflow
		cb := safeRange(b / 3)
		cc := safeRange(c / 3)
		return ca.Add(cb).Add(cc) == ca.Add(cb.Add(cc))
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 10000}); err != nil {
		t.Errorf("associatividade falhou: %v", err)
	}
}

// TestProperty_MulIntDivIntInverse verifica c.MulInt(n).DivInt(n) == (c, 0).
func TestProperty_MulIntDivIntInverse(t *testing.T) {
	prop := func(c int64, n uint8) bool {
		if n == 0 {
			return true
		}
		// Restringe c para nao overflow ao multiplicar.
		cv := Cents(c % 1_000_000_000_000) // ate 1 trilhao
		divisor := int64(n)
		mulled := cv.MulInt(divisor)
		q, r := mulled.DivInt(divisor)
		return q == cv && r == 0
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 10000}); err != nil {
		t.Errorf("MulInt.DivInt nao e inverso: %v", err)
	}
}

// TestProperty_StringRoundTrip verifica FromString(c.String()) == c.
func TestProperty_StringRoundTrip(t *testing.T) {
	prop := func(c int64) bool {
		cv := safeRange(c)
		s := cv.String()
		back, err := FromString(s)
		if err != nil {
			t.Logf("FromString(%q) erro: %v", s, err)
			return false
		}
		return back == cv
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 10000}); err != nil {
		t.Errorf("round-trip String falhou: %v", err)
	}
}

// TestProperty_FormatBRRoundTrip verifica FromString(c.FormatBR()) == c.
func TestProperty_FormatBRRoundTrip(t *testing.T) {
	prop := func(c int64) bool {
		cv := safeRange(c)
		s := cv.FormatBR()
		back, err := FromString(s)
		if err != nil {
			t.Logf("FromString BR(%q) erro: %v", s, err)
			return false
		}
		return back == cv
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 10000}); err != nil {
		t.Errorf("round-trip FormatBR falhou: %v", err)
	}
}

// TestProperty_NegateInvolution verifica c.Negate().Negate() == c.
func TestProperty_NegateInvolution(t *testing.T) {
	prop := func(c int64) bool {
		cv := safeRange(c)
		return cv.Negate().Negate() == cv
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 10000}); err != nil {
		t.Errorf("Negate.Negate nao e identidade: %v", err)
	}
}

// TestProperty_AbsAlwaysNonNegative verifica que Abs sempre retorna >= 0
// (exceto MinInt64 saturado que e MaxInt64, ainda > 0).
func TestProperty_AbsAlwaysNonNegative(t *testing.T) {
	prop := func(c int64) bool {
		cv := safeRange(c)
		return cv.Abs() >= 0
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 10000}); err != nil {
		t.Errorf("Abs retornou negativo: %v", err)
	}
}

// TestProperty_DivIntReconstruction verifica c == q*n + r para qualquer DivInt.
func TestProperty_DivIntReconstruction(t *testing.T) {
	prop := func(c int64, n uint8) bool {
		if n == 0 {
			return true
		}
		cv := Cents(c % 1_000_000_000_000)
		divisor := int64(n)
		q, r := cv.DivInt(divisor)
		return q.MulInt(divisor).Add(r) == cv
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 10000}); err != nil {
		t.Errorf("reconstrucao DivInt falhou: %v", err)
	}
}

// safeRange limita um int64 arbitrario para [-10^15, 10^15] (10 quatrilhoes
// de centavos = 100 trilhoes de reais, faixa amplamente suficiente para evitar
// overflow em testes property-based de Add/Sub).
func safeRange(v int64) Cents {
	const limit = int64(1_000_000_000_000_000)
	if v > 0 {
		return Cents(v % limit)
	}
	if v < 0 {
		return Cents(-((-v) % limit))
	}
	return 0
}

// rng dedicado pra testes determinsticos (nao usado diretamente por quick mas
// reservado para futuras extensoes).
var rng = rand.New(rand.NewSource(1)) //nolint:gosec // teste, nao precisa CSPRNG.

var _ = rng // evitar warning de variavel nao usada
