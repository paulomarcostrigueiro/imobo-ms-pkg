// Copyright 2026 imobo. Licenca: privada.

// Package iauso registra e controla TODO uso da API da Anthropic na IMOBO.
//
// # O que faz
//
// Fica no meio do caminho entre o servico e a Anthropic, como um
// http.RoundTripper. Para cada POST /v1/messages:
//
//  1. descobre de onde veio (servico, funcionalidade, tenant, conversa) e de qual
//     organizacao da Anthropic e a chave (ou se e a chave propria do tenant);
//  2. confere o teto mensal da organizacao e, se ja estourou, devolve 429 sem
//     chamar a Anthropic;
//  3. chama a Anthropic (ou o ia-service, conforme o modo);
//  4. le o `usage` da resposta, calcula o custo com o preco do modelo
//     (claudeia) e grava um Registro, sem o conteudo das mensagens (LGPD);
//  5. avisa quando a organizacao passa de 80% e de 100% do teto.
//
// # Os dois modos (IMOBO_IA_MODO)
//
//   - biblioteca: o servico chama a Anthropic direto e grava no PROPRIO banco.
//   - microsservico: o servico manda o pedido ao ia-service, que faz os passos
//     2 a 5 com ESTE MESMO pacote (ver Proxy). Se o ia-service nao CONECTAR, o
//     servico chama a Anthropic direto e grava localmente (plano B, modo
//     "plano_b" no registro).
//   - desligado: passa direto, sem registrar nem conferir teto (emergencia).
//
// A logica de custo, teto e registro e uma so nos dois modos; trocar de modo e
// mudar uma variavel de ambiente e reiniciar o servico. A documentacao de
// operacao fica em docs/arquitetura/IA-USO-BIBLIOTECA-E-MICROSSERVICO.md do
// repositorio central.
//
// # Regras fixas
//
//   - A IA NUNCA para por falha do controle: banco fora do ar, consulta de gasto
//     falhando ou fila cheia so geram log. Quem para a IA e so o teto atingido.
//   - O registro nunca leva texto de mensagem, prompt ou chave. A chave so e
//     guardada em memoria como sha256, para identificar a organizacao.
//   - Plano B so quando nao CONECTA no ia-service. Se conectou e demorou, nao
//     repete direto: o pedido pode ja ter chegado a Anthropic e seria cobrado
//     duas vezes.
package iauso
