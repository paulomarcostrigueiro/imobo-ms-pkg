// Copyright 2026 imobo. Licenca: privada.

package phonebr

import "testing"

func TestClassify(t *testing.T) {
	t.Parallel()
	casos := []struct {
		nome     string
		in       string
		wantE164 string
		wantSt   Status
		wantVal  bool
	}{
		// --- O bug real do Gabriel: "0" de tronco, sem 55 -------------------
		{"planilha_zero_tronco_celular", "051989483791", "5551989483791", StatusValido, true},
		{"celular_11dig_sem_55", "51989483791", "5551989483791", StatusValido, true},
		{"fixo_ou_antigo_10dig_sem_55", "5189483791", "555189483791", StatusValido, true},
		// --- Ja em E.164 (idempotente) --------------------------------------
		{"ja_e164_13dig", "5551989483791", "5551989483791", StatusValido, true},
		{"ja_e164_12dig_oito", "555189483791", "555189483791", StatusValido, true},
		{"com_mais_e_simbolos", "+55 (51) 98948-3791", "5551989483791", StatusValido, true},
		{"multiplos_zeros_tronco", "00051989483791", "5551989483791", StatusValido, true},
		// --- Invalidos ------------------------------------------------------
		{"vazio", "", "", StatusSemTelefone, false},
		{"so_simbolos", "()-  +", "", StatusSemTelefone, false},
		{"so_zeros", "0000", "", StatusSemTelefone, false},
		{"sem_ddd_9dig", "989483791", "", StatusIncompleto, false},
		{"sem_ddd_8dig", "89483791", "", StatusIncompleto, false},
		{"muito_curto", "1234567", "", StatusFormatoInvalido, false},
		{"ddd_invalido_10", "1089483791", "", StatusDDDInvalido, false},
		{"ddd_invalido_11", "10989483791", "", StatusDDDInvalido, false},
		{"ddd_invalido_pos55", "5510989483791", "", StatusDDDInvalido, false},
		{"codigo_pais_nao_br_12", "351912345678", "", StatusFormatoInvalido, false},
		{"digitos_demais_14", "55519894837911", "", StatusFormatoInvalido, false},
	}
	for _, c := range casos {
		c := c
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			got := Classify(c.in)
			if got.E164 != c.wantE164 {
				t.Errorf("Classify(%q).E164 = %q; quer %q", c.in, got.E164, c.wantE164)
			}
			if got.Status != c.wantSt {
				t.Errorf("Classify(%q).Status = %q; quer %q", c.in, got.Status, c.wantSt)
			}
			if got.Valido != c.wantVal {
				t.Errorf("Classify(%q).Valido = %v; quer %v", c.in, got.Valido, c.wantVal)
			}
			if got.Valido && got.Motivo != "" {
				t.Errorf("Classify(%q) valido mas Motivo nao-vazio: %q", c.in, got.Motivo)
			}
			if !got.Valido && got.Motivo == "" {
				t.Errorf("Classify(%q) invalido mas Motivo vazio", c.in)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()
	if got := Normalize("051989483791"); got != "5551989483791" {
		t.Errorf("Normalize = %q; quer 5551989483791", got)
	}
	if got := Normalize("sem digito"); got != "" {
		t.Errorf("Normalize(invalido) = %q; quer vazio", got)
	}
}

// TestNormalizeIdempotente: normalizar duas vezes nao muda o resultado — garante
// que aplicar no choke-point e seguro mesmo se o numero ja veio normalizado.
func TestNormalizeIdempotente(t *testing.T) {
	t.Parallel()
	entradas := []string{
		"051989483791", "51989483791", "5189483791", "5551989483791",
		"555189483791", "+55 (51) 98948-3791",
	}
	for _, in := range entradas {
		um := Normalize(in)
		dois := Normalize(um)
		if um != dois {
			t.Errorf("nao idempotente para %q: %q -> %q", in, um, dois)
		}
		if um != "" && !Valido(um) {
			t.Errorf("Normalize(%q)=%q deveria ser Valido", in, um)
		}
	}
}

func TestValido(t *testing.T) {
	t.Parallel()
	if !Valido("051989483791") {
		t.Error("Valido(planilha) deveria ser true")
	}
	if Valido("989483791") {
		t.Error("Valido(sem ddd) deveria ser false")
	}
}

func TestDDDValido(t *testing.T) {
	t.Parallel()
	if !dddValido("51") || !dddValido("11") || !dddValido("99") {
		t.Error("DDDs validos rejeitados")
	}
	if dddValido("10") || dddValido("20") || dddValido("00") || dddValido("5") || dddValido("551") {
		t.Error("DDDs invalidos aceitos")
	}
}
