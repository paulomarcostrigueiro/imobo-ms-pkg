// Copyright 2026 imobo. Licença: privada.

package moneycents

import (
	"errors"
	"math"
	"strings"
	"testing"
)

// TestFromString_HappyPath cobre os casos comuns de entrada.
func TestFromString_HappyPath(t *testing.T) {
	cases := []struct {
		in   string
		want Cents
	}{
		{"1234.56", 123456},
		{"1234,56", 123456},
		{"1.234,56", 123456}, // BR com milhar
		{"1,234.56", 123456}, // US com milhar
		{"0", 0},
		{"0.00", 0},
		{"0,00", 0},
		{"0.01", 1},
		{"0,01", 1},
		{"0.10", 10},
		{"0.1", 10}, // pad de 1 casa pra 2
		{"100", 10000},
		{"100.5", 10050},
		{"-1234,56", -123456},
		{"-0.01", -1},
		{"+1234.56", 123456},        // sinal positivo explicito
		{"  1234.56  ", 123456},     // espacos
		{"1.000.000,00", 100000000}, // 1 milhao em BR
		{"1,000,000.00", 100000000}, // 1 milhao em US
		{",50", 50},                 // virgula sem inteiro = 0,50
		{".50", 50},
	}
	for _, tc := range cases {
		got, err := FromString(tc.in)
		if err != nil {
			t.Errorf("FromString(%q) erro inesperado: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("FromString(%q) = %d, esperado %d", tc.in, got, tc.want)
		}
	}
}

// TestFromString_Errors cobre os casos de erro.
func TestFromString_Errors(t *testing.T) {
	cases := []struct {
		in   string
		want error
	}{
		{"", ErrEmptyString},
		{"   ", ErrEmptyString},
		{"abc", ErrInvalidFormat},
		{"12a3.45", ErrInvalidFormat},
		{"12.3a", ErrInvalidFormat},
		{"123.456", ErrTooManyDecimals},
		{"99999999999999999999999.00", ErrOverflow},
		{"-", ErrInvalidFormat},
		{"+", ErrInvalidFormat},
	}
	for _, tc := range cases {
		_, err := FromString(tc.in)
		if !errors.Is(err, tc.want) {
			t.Errorf("FromString(%q) erro = %v, esperado %v", tc.in, err, tc.want)
		}
	}
}

// TestFromString_OverflowOnInteger garante deteccao de overflow no parsing.
func TestFromString_OverflowOnInteger(t *testing.T) {
	// Ate 92 quatrilhoes de reais e ok. Acima, overflow.
	huge := strings.Repeat("9", 30) + ".00"
	_, err := FromString(huge)
	if !errors.Is(err, ErrOverflow) {
		t.Errorf("esperava ErrOverflow para %q, recebeu %v", huge, err)
	}
}

// TestFromString_OverflowOnAddition cobre o ramo onde intVal*100+decVal estoura.
func TestFromString_OverflowOnAddition(t *testing.T) {
	// math.MaxInt64 / 100 = 92233720368547758 (aprox), entao um numero com 18
	// digitos inteiros + 99 centavos pode estourar.
	cases := []string{
		"92233720368547758.99",
		"92233720368547759.00",
	}
	for _, in := range cases {
		_, err := FromString(in)
		if !errors.Is(err, ErrOverflow) {
			t.Errorf("FromString(%q) esperava ErrOverflow, recebeu %v", in, err)
		}
	}
}

// TestFromString_MultipleSeparators cobre formatos com so milhar (sem decimal).
//
// Casos: "1,000,000" (US milhar, sem decimal), "1.000.000" (BR milhar, sem
// decimal). Heuristica: multiplos separadores iguais sem o oposto = milhar.
func TestFromString_MultipleSeparators(t *testing.T) {
	cases := []struct {
		in   string
		want Cents
	}{
		{"1,000,000", 100000000},   // US milhar, sem decimal
		{"1.000.000", 100000000},   // BR milhar, sem decimal
		{"10,000,000", 1000000000}, // 10 milhoes US milhar
		{"10.000.000", 1000000000}, // 10 milhoes BR milhar
	}
	for _, tc := range cases {
		got, err := FromString(tc.in)
		if err != nil {
			t.Errorf("FromString(%q) erro: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("FromString(%q) = %d, esperado %d", tc.in, got, tc.want)
		}
	}
}

// TestFromReais cobre conversao de float64.
func TestFromReais(t *testing.T) {
	cases := []struct {
		in   float64
		want Cents
	}{
		{0, 0},
		{1.0, 100},
		{1.50, 150},
		{1234.56, 123456},
		{0.01, 1},
		{0.1 + 0.2, 30}, // 0.30000000000000004 arredondado
		{-1.5, -150},
		{99.999, 10000}, // arredonda meio-pra-cima
	}
	for _, tc := range cases {
		got := FromReais(tc.in)
		if got != tc.want {
			t.Errorf("FromReais(%v) = %d, esperado %d", tc.in, got, tc.want)
		}
	}
}

// TestString cobre formatacao decimal canonica.
func TestString(t *testing.T) {
	cases := []struct {
		in   Cents
		want string
	}{
		{0, "0.00"},
		{1, "0.01"},
		{10, "0.10"},
		{100, "1.00"},
		{123456, "1234.56"},
		{-123456, "-1234.56"},
		{-1, "-0.01"},
	}
	for _, tc := range cases {
		got := tc.in.String()
		if got != tc.want {
			t.Errorf("Cents(%d).String() = %q, esperado %q", tc.in, got, tc.want)
		}
	}
}

// TestFormatBR cobre formatacao brasileira.
func TestFormatBR(t *testing.T) {
	cases := []struct {
		in   Cents
		want string
	}{
		{0, "0,00"},
		{1, "0,01"},
		{100, "1,00"},
		{123456, "1.234,56"},
		{100000000, "1.000.000,00"},
		{-123456, "-1.234,56"},
		{1000, "10,00"},
		{99999, "999,99"},
		{1000000, "10.000,00"},
	}
	for _, tc := range cases {
		got := tc.in.FormatBR()
		if got != tc.want {
			t.Errorf("Cents(%d).FormatBR() = %q, esperado %q", tc.in, got, tc.want)
		}
	}
}

// TestFormatBRCurrency cobre formatacao com simbolo R$.
func TestFormatBRCurrency(t *testing.T) {
	cases := []struct {
		in   Cents
		want string
	}{
		{0, "R$ 0,00"},
		{123456, "R$ 1.234,56"},
		{-123456, "-R$ 1.234,56"},
		{1, "R$ 0,01"},
	}
	for _, tc := range cases {
		got := tc.in.FormatBRCurrency()
		if got != tc.want {
			t.Errorf("Cents(%d).FormatBRCurrency() = %q, esperado %q", tc.in, got, tc.want)
		}
	}
}

// TestRoundTrip garante que FromString -> String preserva valor.
func TestRoundTrip(t *testing.T) {
	values := []Cents{0, 1, 10, 100, 123456, -123456, 100000000, 999999999, -1}
	for _, v := range values {
		s := v.String()
		back, err := FromString(s)
		if err != nil {
			t.Errorf("FromString(%q) erro: %v", s, err)
			continue
		}
		if back != v {
			t.Errorf("round-trip falhou: %d -> %q -> %d", v, s, back)
		}
	}
}

// TestRoundTripBR garante round-trip com formatacao brasileira.
func TestRoundTripBR(t *testing.T) {
	values := []Cents{0, 1, 100, 123456, -123456, 100000000}
	for _, v := range values {
		s := v.FormatBR()
		back, err := FromString(s)
		if err != nil {
			t.Errorf("FromString(%q) erro: %v", s, err)
			continue
		}
		if back != v {
			t.Errorf("round-trip BR falhou: %d -> %q -> %d", v, s, back)
		}
	}
}

// TestAdd cobre soma e overflow.
func TestAdd(t *testing.T) {
	if got := Cents(100).Add(50); got != 150 {
		t.Errorf("Add basico falhou: %d", got)
	}
	if got := Cents(-100).Add(50); got != -50 {
		t.Errorf("Add negativo falhou: %d", got)
	}
	if got := Cents(0).Add(0); got != 0 {
		t.Errorf("Add zero falhou: %d", got)
	}

	// Overflow deve panicar.
	defer func() {
		if r := recover(); r == nil {
			t.Error("Add deveria panicar em overflow")
		}
	}()
	_ = Cents(math.MaxInt64).Add(Cents(1))
}

// TestAdd_NegativeOverflow cobre underflow.
func TestAdd_NegativeOverflow(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Add deveria panicar em underflow")
		}
	}()
	_ = Cents(math.MinInt64).Add(Cents(-1))
}

// TestSub cobre subtracao.
func TestSub(t *testing.T) {
	if got := Cents(100).Sub(30); got != 70 {
		t.Errorf("Sub basico falhou: %d", got)
	}
	if got := Cents(50).Sub(100); got != -50 {
		t.Errorf("Sub negativo falhou: %d", got)
	}
	if got := Cents(0).Sub(0); got != 0 {
		t.Errorf("Sub zero falhou: %d", got)
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("Sub deveria panicar em overflow")
		}
	}()
	_ = Cents(math.MaxInt64).Sub(Cents(-1))
}

// TestSub_NegativeOverflow cobre underflow em sub.
func TestSub_NegativeOverflow(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Sub deveria panicar em underflow")
		}
	}()
	_ = Cents(math.MinInt64).Sub(Cents(1))
}

// TestMulInt cobre multiplicacao por inteiro.
func TestMulInt(t *testing.T) {
	cases := []struct {
		c    Cents
		n    int64
		want Cents
	}{
		{100, 12, 1200},     // 12x R$ 1,00 = R$ 12,00
		{50000, 12, 600000}, // 12x R$ 500,00 = R$ 6.000,00
		{0, 100, 0},
		{100, 0, 0},
		{-100, 5, -500},
		{100, -5, -500},
	}
	for _, tc := range cases {
		got := tc.c.MulInt(tc.n)
		if got != tc.want {
			t.Errorf("Cents(%d).MulInt(%d) = %d, esperado %d", tc.c, tc.n, got, tc.want)
		}
	}

	// Overflow.
	defer func() {
		if r := recover(); r == nil {
			t.Error("MulInt deveria panicar em overflow")
		}
	}()
	_ = Cents(math.MaxInt64 / 2).MulInt(3)
}

// TestDivInt cobre divisao com resto (rateio entre socios).
func TestDivInt(t *testing.T) {
	// Caso classico: R$ 100,00 / 3 socios.
	q, r := Cents(10000).DivInt(3)
	if q != 3333 {
		t.Errorf("quociente esperado 3333, obtido %d", q)
	}
	if r != 1 {
		t.Errorf("resto esperado 1 centavo, obtido %d", r)
	}
	// Reconstrucao: 3333*3 + 1 = 10000.
	if q.MulInt(3).Add(r) != 10000 {
		t.Errorf("rateio nao bate: %d * 3 + %d != 10000", q, r)
	}

	// Divisao exata.
	q, r = Cents(10000).DivInt(4)
	if q != 2500 || r != 0 {
		t.Errorf("R$ 100/4 esperava (2500,0), obtido (%d,%d)", q, r)
	}

	// Zero dividido.
	q, r = Cents(0).DivInt(5)
	if q != 0 || r != 0 {
		t.Errorf("0/5 esperava (0,0), obtido (%d,%d)", q, r)
	}
}

// TestDivInt_DivisionByZero garante panic em divisao por zero.
func TestDivInt_DivisionByZero(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("DivInt(0) deveria panicar")
		}
	}()
	_, _ = Cents(100).DivInt(0)
}

// TestMulPercent cobre calculo percentual em basis points.
func TestMulPercent(t *testing.T) {
	cases := []struct {
		c    Cents
		bps  int
		want Cents
	}{
		// Casos basicos.
		{100000, 800, 8000},   // 8% de R$ 1.000,00 = R$ 80,00
		{10000, 1500, 1500},   // 15% de R$ 100,00 = R$ 15,00
		{10000, 10000, 10000}, // 100% de R$ 100,00 = R$ 100,00
		{10000, 100, 100},     // 1% de R$ 100,00 = R$ 1,00
		{10000, 50, 50},       // 0.5% de R$ 100,00 = R$ 0,50
		{0, 800, 0},
		{100000, 0, 0},

		// Caso classico: rateio 33,33% / 33,33% / 33,34%.
		// R$ 100,00 * 3333 bps = 3333 centavos (R$ 33,33).
		{10000, 3333, 3333},
		{10000, 3334, 3334},
	}
	for _, tc := range cases {
		got := tc.c.MulPercent(tc.bps)
		if got != tc.want {
			t.Errorf("Cents(%d).MulPercent(%d) = %d, esperado %d",
				tc.c, tc.bps, got, tc.want)
		}
	}
}

// TestMulPercent_Overflow garante panic em overflow.
func TestMulPercent_Overflow(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MulPercent deveria panicar em overflow")
		}
	}()
	_ = Cents(math.MaxInt64).MulPercent(20000)
}

// TestRateioADR001 reproduz o caso da ADR-001: rateio 33.33%/33.33%/33.34%.
//
// Cenario: R$ 100,00 entre 3 socios A/B/C. Politica: A e B recebem 33,33%
// (3333 bps), C recebe o resto (33,34% = 3334 bps).
func TestRateioADR001(t *testing.T) {
	total := Cents(10000) // R$ 100,00
	a := total.MulPercent(3333)
	b := total.MulPercent(3333)
	c := total.MulPercent(3334)
	soma := a.Add(b).Add(c)
	if soma != total {
		t.Errorf("rateio ADR-001 nao bate: %d + %d + %d = %d (esperado %d)",
			a, b, c, soma, total)
	}
	if a != 3333 || b != 3333 || c != 3334 {
		t.Errorf("rateio ADR-001: a=%d b=%d c=%d, esperado 3333/3333/3334",
			a, b, c)
	}
}

// TestPredicates cobre IsZero, IsPositive, IsNegative.
func TestPredicates(t *testing.T) {
	if !Cents(0).IsZero() {
		t.Error("0 deveria ser IsZero")
	}
	if Cents(1).IsZero() {
		t.Error("1 nao deveria ser IsZero")
	}
	if !Cents(1).IsPositive() {
		t.Error("1 deveria ser IsPositive")
	}
	if Cents(0).IsPositive() {
		t.Error("0 nao deveria ser IsPositive")
	}
	if Cents(-1).IsPositive() {
		t.Error("-1 nao deveria ser IsPositive")
	}
	if !Cents(-1).IsNegative() {
		t.Error("-1 deveria ser IsNegative")
	}
	if Cents(0).IsNegative() {
		t.Error("0 nao deveria ser IsNegative")
	}
	if Cents(1).IsNegative() {
		t.Error("1 nao deveria ser IsNegative")
	}
}

// TestAbs cobre valor absoluto.
func TestAbs(t *testing.T) {
	if Cents(100).Abs() != 100 {
		t.Error("Abs de positivo deve ser igual")
	}
	if Cents(-100).Abs() != 100 {
		t.Error("Abs de negativo deve inverter sinal")
	}
	if Cents(0).Abs() != 0 {
		t.Error("Abs(0) = 0")
	}
	// Caso especial MinInt64.
	if Cents(math.MinInt64).Abs() != Cents(math.MaxInt64) {
		t.Error("Abs(MinInt64) deve saturar em MaxInt64")
	}
}

// TestNegate cobre inversao de sinal.
func TestNegate(t *testing.T) {
	if Cents(100).Negate() != -100 {
		t.Error("Negate(100) = -100")
	}
	if Cents(-100).Negate() != 100 {
		t.Error("Negate(-100) = 100")
	}
	if Cents(0).Negate() != 0 {
		t.Error("Negate(0) = 0")
	}
	if Cents(math.MinInt64).Negate() != Cents(math.MaxInt64) {
		t.Error("Negate(MinInt64) deve saturar em MaxInt64")
	}
}

// TestSplitAluguelMultiProprietarios reproduz cenario real: aluguel R$ 2.500,00
// dividido entre 2 proprietarios em 60%/40%.
func TestSplitAluguelMultiProprietarios(t *testing.T) {
	aluguel := Cents(250000)          // R$ 2.500,00
	prop1 := aluguel.MulPercent(6000) // 60%
	prop2 := aluguel.MulPercent(4000) // 40%
	if prop1.Add(prop2) != aluguel {
		t.Errorf("split 60/40 nao bate: %d + %d != %d", prop1, prop2, aluguel)
	}
	if prop1 != 150000 || prop2 != 100000 {
		t.Errorf("split 60/40 esperava 150000/100000, obteve %d/%d", prop1, prop2)
	}
}

// TestSplitAluguelComResto reproduz cenario com resto: R$ 1.000,00 em 3 partes iguais.
func TestSplitAluguelComResto(t *testing.T) {
	aluguel := Cents(100000) // R$ 1.000,00
	q, r := aluguel.DivInt(3)
	// Politica: dar a sobra ao primeiro socio.
	primeiro := q.Add(r)
	segundo := q
	terceiro := q
	soma := primeiro.Add(segundo).Add(terceiro)
	if soma != aluguel {
		t.Errorf("split 3 partes nao bate: %d + %d + %d = %d (esperado %d)",
			primeiro, segundo, terceiro, soma, aluguel)
	}
}

// --- Testes JSON ---

func TestMarshalJSON(t *testing.T) {
	cases := []struct {
		in   Cents
		want string
	}{
		{0, "0.00"},
		{123456, "1234.56"},
		{-123456, "-1234.56"},
		{1, "0.01"},
	}
	for _, tc := range cases {
		got, err := tc.in.MarshalJSON()
		if err != nil {
			t.Errorf("MarshalJSON(%d) erro: %v", tc.in, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("MarshalJSON(%d) = %q, esperado %q", tc.in, got, tc.want)
		}
	}
}

func TestUnmarshalJSON(t *testing.T) {
	cases := []struct {
		in   string
		want Cents
	}{
		{`1234.56`, 123456},
		{`"1234.56"`, 123456},
		{`"1234,56"`, 123456},
		{`"1.234,56"`, 123456},
		{`0`, 0},
		{`null`, 0},
		{`-1234.56`, -123456},
		{`1e3`, 100000}, // 1000 reais via notacao cientifica
	}
	for _, tc := range cases {
		var c Cents
		if err := c.UnmarshalJSON([]byte(tc.in)); err != nil {
			t.Errorf("UnmarshalJSON(%q) erro: %v", tc.in, err)
			continue
		}
		if c != tc.want {
			t.Errorf("UnmarshalJSON(%q) = %d, esperado %d", tc.in, c, tc.want)
		}
	}
}

func TestUnmarshalJSON_Errors(t *testing.T) {
	cases := []string{
		``,
		`abc`,
		`"abc"`,
		`{}`,
		`"   "`,
	}
	for _, tc := range cases {
		var c Cents
		if err := c.UnmarshalJSON([]byte(tc)); err == nil {
			t.Errorf("UnmarshalJSON(%q) deveria falhar", tc)
		}
	}
}

// TestUnmarshalJSON_BadScientific cobre notacao cientifica invalida.
func TestUnmarshalJSON_BadScientific(t *testing.T) {
	var c Cents
	if err := c.UnmarshalJSON([]byte(`1e`)); err == nil {
		t.Error("notacao cientifica invalida deveria falhar")
	}
}

// --- Testes SQL ---

func TestScan(t *testing.T) {
	cases := []struct {
		in   interface{}
		want Cents
	}{
		{int64(123456), 123456},
		{int32(123456), 123456},
		{int(123456), 123456},
		{nil, 0},
		{"1234.56", 123456},
		{[]byte("1234.56"), 123456},
		{[]byte("1234,56"), 123456},
	}
	for _, tc := range cases {
		var c Cents
		if err := c.Scan(tc.in); err != nil {
			t.Errorf("Scan(%v) erro: %v", tc.in, err)
			continue
		}
		if c != tc.want {
			t.Errorf("Scan(%v) = %d, esperado %d", tc.in, c, tc.want)
		}
	}
}

func TestScan_Errors(t *testing.T) {
	var c Cents
	if err := c.Scan(3.14); err == nil {
		t.Error("Scan(float) deveria falhar")
	}
	if err := c.Scan("nao e numero"); err == nil {
		t.Error("Scan(string invalida) deveria falhar")
	}
	if err := c.Scan([]byte("xyz")); err == nil {
		t.Error("Scan([]byte invalido) deveria falhar")
	}
}

func TestValue(t *testing.T) {
	c := Cents(123456)
	v, err := c.Value()
	if err != nil {
		t.Errorf("Value() erro: %v", err)
	}
	got, ok := v.(int64)
	if !ok {
		t.Errorf("Value() deveria retornar int64, obteve %T", v)
	}
	if got != 123456 {
		t.Errorf("Value() = %d, esperado 123456", got)
	}
}
