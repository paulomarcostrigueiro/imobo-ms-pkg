// Copyright 2026 imobo. Licenca: privada.

// Sprint 16 D — propriedades comutativa/associativa/identidade reformuladas
// usando pgregory.net/rapid (complementa cents_property_test.go que usa
// testing/quick). rapid oferece shrinking de contraexemplos.

package moneycents_test

import (
	"testing"

	"pgregory.net/rapid"

	"github.com/paulomarcostrigueiro/imobo-ms-pkg/moneycents"
)

// safeRange limita valores para evitar overflow int64 em soma.
func safeRange(t *rapid.T, label string) moneycents.Cents {
	v := rapid.Int64Range(-1_000_000_000, 1_000_000_000).Draw(t, label)
	return moneycents.Cents(v)
}

func TestRapid_AddCommutative(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := safeRange(t, "a")
		b := safeRange(t, "b")
		if a.Add(b) != b.Add(a) {
			t.Fatalf("a+b != b+a: a=%d b=%d", a, b)
		}
	})
}

func TestRapid_AddAssociative(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := safeRange(t, "a")
		b := safeRange(t, "b")
		c := safeRange(t, "c")
		if a.Add(b).Add(c) != a.Add(b.Add(c)) {
			t.Fatalf("(a+b)+c != a+(b+c): a=%d b=%d c=%d", a, b, c)
		}
	})
}

func TestRapid_SubInverseAdd(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := safeRange(t, "a")
		b := safeRange(t, "b")
		// a + b - b == a
		if a.Add(b).Sub(b) != a {
			t.Fatalf("(a+b)-b != a: a=%d b=%d", a, b)
		}
	})
}

func TestRapid_NegInverso(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := safeRange(t, "a")
		// -(-a) == a
		if -(-a) != a {
			t.Fatalf("-(-a) != a: %d", a)
		}
	})
}

func TestRapid_AddZeroIdentity(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := safeRange(t, "a")
		zero := moneycents.Cents(0)
		if a.Add(zero) != a || zero.Add(a) != a {
			t.Fatalf("identidade aditiva falhou: %d", a)
		}
	})
}
