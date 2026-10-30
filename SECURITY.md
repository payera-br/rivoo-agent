# Segurança

Encontrou uma falha no agente (vazamento de senha ou chave, execução de comando indevida, atualização
sem assinatura)? Não abra issue pública: use **Security → Report a vulnerability** neste repositório
(relato privado do GitHub).

O que o agente garante por desenho:

- Só conexões de saída: para a API da Rivoo (HTTPS) e para o console do jogo.
- A senha do RCON e a chave da conexão ficam no arquivo local (`0600`) ou em variáveis de ambiente;
  nunca vão para log, mensagem de erro ou heartbeat.
- Atualizações só são instaladas se o manifesto tiver assinatura ed25519 válida da Rivoo e o
  binário bater com o checksum do manifesto.
- No modo nuvem, o worker recusa endereços de rede interna, inclusive depois de resolver o DNS.
