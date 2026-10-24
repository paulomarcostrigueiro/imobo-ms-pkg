// Copyright 2026 imobo. Licenca: privada.

package phonebr

import (
	"reflect"
	"testing"
)

func TestChaveLocal(t *testing.T) {
	casos := map[string]string{
		"":                      "",
		"(11) 94005-2300":       "11940052300",
		"+55 (11) 94005-2300":   "11940052300",
		"5511940052300":         "11940052300",
		"551140052300":          "1140052300",
		"011 94005-2300":        "11940052300", // tronco
		"0 15 11 94005-2300":    "11940052300", // tronco + operadora
		"+55 0 11 94005-2300":   "11940052300", // DDI + tronco
		"94005-2300":            "940052300",   // sem DDD: fica como veio
		"64 9225-5801":          "6492255801",  // nono digito NAO e mexido aqui
		"abc":                   "",
		"  +55 64 99225-5801  ": "64992255801",
	}
	for in, want := range casos {
		if got := ChaveLocal(in); got != want {
			t.Errorf("ChaveLocal(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestChave(t *testing.T) {
	casos := map[string]string{
		"(11) 94005-2300":      "5511940052300",
		"+55 11 94005-2300":    "5511940052300",
		"11 94005 2300":        "5511940052300",
		"011-94005-2300":       "5511940052300",
		"(64) 9225-5801":       "5564992255801", // celular antigo ganha o 9
		"5564992255801":        "5564992255801",
		"556492255801":         "5564992255801", // Meta sem o nono digito
		"(64) 3621-1234":       "556436211234",  // fixo: 8 digitos, sem 9
		"94005-2300":           "",              // sem DDD
		"(20) 94005-2300":      "",              // DDD inexistente
		"(11) 84005-2300":      "",              // 9 digitos sem comecar com 9
		"+1 415 555 2671":      "",              // outro pais
		"":                     "",
		"1194005230":           "5511994005230", // 10 digitos com 9 inicial = celular antigo
		"+55 (11) 9 4005-2300": "5511940052300",
	}
	for in, want := range casos {
		if got := Chave(in); got != want {
			t.Errorf("Chave(%q) = %q; want %q", in, got, want)
		}
	}
	if got := ChaveComDDD("94005-2300", "11"); got != "5511940052300" {
		t.Errorf("ChaveComDDD sem DDD: %q", got)
	}
	if got := ChaveComDDD("94005-2300", "20"); got != "" {
		t.Errorf("ChaveComDDD com DDD invalido deveria ser vazio: %q", got)
	}
}

func TestVariantes(t *testing.T) {
	casos := map[string][]string{
		"(64) 9225-5801":  {"64992255801", "6492255801"},
		"5564992255801":   {"64992255801", "6492255801"},
		"(11) 94005-2300": {"11940052300"}, // 9 + 4: nao e celular classico
		"(64) 3621-1234":  {"6436211234"},
		"94005-2300":      {"940052300"}, // nao plausivel: so a chave local
		"":                nil,
	}
	for in, want := range casos {
		if got := Variantes(in); !reflect.DeepEqual(got, want) {
			t.Errorf("Variantes(%q) = %v; want %v", in, got, want)
		}
	}
}

func TestMesmo(t *testing.T) {
	iguais := [][2]string{
		{"(11) 94005-2300", "5511940052300"},
		{"+55 11 94005-2300", "11940052300"},
		{"(64) 9225-5801", "5564992255801"},  // nono digito
		{"556492255801", "(64) 99225-5801"},  // Meta sem o 9
		{"94005-2300", "(11) 94005-2300"},    // DDD ausente de um lado
		{"011 94005 2300", "+5511940052300"}, // tronco
	}
	for _, p := range iguais {
		if !Mesmo(p[0], p[1]) || !Mesmo(p[1], p[0]) {
			t.Errorf("Mesmo(%q, %q) deveria ser true", p[0], p[1])
		}
	}
	// Controle negativo: o caso real (1 digito trocado) NAO e o mesmo.
	diferentes := [][2]string{
		{"(11) 94405-2300", "(11) 94005-2300"},
		{"(11) 94005-2300", "(21) 94005-2300"}, // outro DDD
		{"", ""},
		{"abc", "abc"},
		{"94005-2300", "94005-2300"}, // sem DDD dos dois lados: nao afirma
	}
	for _, p := range diferentes {
		if Mesmo(p[0], p[1]) {
			t.Errorf("Mesmo(%q, %q) deveria ser false", p[0], p[1])
		}
	}
}

func TestParecido(t *testing.T) {
	parecidos := [][2]string{
		{"(11) 94405-2300", "5511940052300"}, // caso Cristiane: 1 digito trocado
		{"(11) 94005-2300", "(11) 94005-2300"},
		{"(64) 3621-1234", "(64) 3621-1235"}, // fixo, 1 digito
	}
	for _, p := range parecidos {
		if !Parecido(p[0], p[1]) {
			t.Errorf("Parecido(%q, %q) deveria ser true", p[0], p[1])
		}
	}
	distantes := [][2]string{
		{"(11) 94405-2311", "(11) 94005-2300"}, // 3 digitos
		{"(11) 94005-2300", ""},
		{"lixo", "(11) 94005-2300"},
	}
	for _, p := range distantes {
		if Parecido(p[0], p[1]) {
			t.Errorf("Parecido(%q, %q) deveria ser false", p[0], p[1])
		}
	}
}

func TestDistanciaAteUm(t *testing.T) {
	casos := []struct {
		a, b string
		ok   bool
	}{
		{"123", "123", true},
		{"123", "124", true},
		{"123", "1234", true},
		{"1234", "134", true},
		{"123", "321", false},
		{"12", "1234", false},
		{"", "1", true},
	}
	for _, c := range casos {
		if got := distanciaAteUm(c.a, c.b); got != c.ok {
			t.Errorf("distanciaAteUm(%q,%q) = %v; want %v", c.a, c.b, got, c.ok)
		}
	}
}
