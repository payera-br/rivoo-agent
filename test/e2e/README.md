# Teste ponta a ponta

## Com a API local e um RCON falso (sem servidor de jogo)

1. API local em `http://localhost:4100` (rivoo-api, `npm run start:dev`).
2. `python3 test/e2e/fake_mc_rcon.py /tmp/rcon.log` (porta 25599, senha `pw`, Steve online).
3. `RIVOO_API_URL=http://localhost:4100 RIVOO_GAME=MINECRAFT_JAVA RCON_PORT=25599 RCON_PASSWORD=pw dist/rivoo-agent run`
4. Digite o código no painel (ou `POST /v1/connection-pairings/<código>/claim`).
5. Testar comando no painel → resposta aparece; comando errado → `FAILED` com a mensagem; run para
   Steve sai, para Alex (offline) fica na fila.

Rodado em 30/10/2026 contra a API local: tudo como acima.

## Com servidor real

- Minecraft: `EULA=TRUE docker compose -f test/e2e/docker-compose.yml up` (aceitar a EULA é do
  dono do servidor), agente com `RCON_PORT=25575`.
- Rust, Palworld, 7 Days to Die: imagens grandes (Rust ~9 GB); rodar numa máquina com espaço e
  guardar as saídas reais de `playerlist`, `ShowPlayers` e `lp` em `internal/games/testdata/`.
- ARK, Conan e Hytale ficam "beta" no painel até passarem por aqui.
