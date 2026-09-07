// Copyright 2026 imobo. Licenca: privada.

package certa1

// somenteDigitos remove mascara. O CNPJ no certificado vem ora cru, ora
// formatado, dependendo da AC.
func somenteDigitos(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= '0' && r <= '9' {
			out = append(out, r)
		}
	}
	return string(out)
}

// cnpjPlausivel confere os 14 digitos e os digitos verificadores.
//
// Por que validar DV aqui, se em outros pontos do sistema deliberadamente NAO
// validamos: neste caso o numero nao veio de um humano digitando, veio de um
// campo ASN.1 que pode ter sido extraido do lugar errado. O DV e o que separa
// "achei o CNPJ" de "achei 14 digitos quaisquer".
func cnpjPlausivel(cnpj string) bool {
	if len(cnpj) != 14 {
		return false
	}
	todosIguais := true
	for i := 1; i < 14; i++ {
		if cnpj[i] != cnpj[0] {
			todosIguais = false
			break
		}
	}
	if todosIguais {
		return false
	}
	pesos1 := []int{5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}
	pesos2 := []int{6, 5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}
	dv := func(pesos []int) int {
		soma := 0
		for i, p := range pesos {
			soma += int(cnpj[i]-'0') * p
		}
		r := soma % 11
		if r < 2 {
			return 0
		}
		return 11 - r
	}
	return int(cnpj[12]-'0') == dv(pesos1) && int(cnpj[13]-'0') == dv(pesos2)
}
