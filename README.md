# Agente Rivoo

Entrega as compras da sua loja [Rivoo](https://rivoo.gg) dentro do servidor de jogo, pelo console
remoto do jogo (RCON). Roda ao lado do servidor, busca os comandos de cada compra e executa **só com
o jogador online**. Se o jogo responder com erro, a entrega aparece como falha no painel, com a
resposta do servidor, para você corrigir e reenviar.

- Nenhuma porta aberta: o agente só faz conexões de saída (para a Rivoo e para o RCON).
- A senha do RCON fica só na sua máquina.
- Atualiza sozinho, só com versões assinadas pela Rivoo.

Não consegue rodar nada ao lado do servidor (servidor alugado sem acesso)? No painel, em
**Entrega in-game → Conectar servidor → Não consigo instalar nada**, a Rivoo roda o agente para você.

## Jogos

| Jogo | Console | Porta padrão | Online por | Situação |
|---|---|---|---|---|
| Minecraft Java (com Geyser) | RCON | 25575 | nick | estável |
| Rust | WebRCON | 28016 | SteamID64 | beta |
| Palworld | RCON | 25575 | SteamID64 | beta |
| 7 Days to Die | Telnet | 8081 | SteamID64 | beta |
| ARK: Survival Evolved | RCON | 27020 | SteamID64 | beta |
| ARK: Survival Ascended | RCON | 27020 | nick | beta |
| Conan Exiles | RCON | 25575 | SteamID64 | beta |
| Hytale (mod HyRCON) | RCON | 25575 | — | beta |
| Outro jogo com RCON (Source) | RCON | a sua | — | beta |

"Beta": funciona, mas ainda não passou pelo teste com servidor de verdade (veja
[`test/e2e`](test/e2e/README.md)). Jogos sem lista de jogadores entregam sem checar se o jogador
está online.

## Instalar

### 1. Ligue o RCON no jogo

| Jogo | Onde |
|---|---|
| Minecraft | `server.properties`: `enable-rcon=true`, `rcon.port=25575`, `rcon.password=<senha>` |
| Rust | linha de início: `+rcon.web 1 +rcon.port 28016 +rcon.password "<senha>"` |
| Palworld | `PalWorldSettings.ini`: `RCONEnabled=True`, `RCONPort=25575`, `AdminPassword="<senha>"` |
| 7 Days to Die | `serverconfig.xml`: `TelnetEnabled=true`, `TelnetPort=8081`, `TelnetPassword=<senha>` |
| ARK | linha de início: `?RCONEnabled=True?RCONPort=27020`, `ServerAdminPassword=<senha>` |
| Conan Exiles | `Game.ini`, seção `[RconPlugin]`: `RconEnabled=1`, `RconPort=25575`, `RconPassword=<senha>` |
| Hytale | instale o mod HyRCON e defina a senha dele |

Reinicie o servidor depois.

### 2. Rode o agente

**Linux**

```bash
curl -fsSL -o rivoo-agent https://github.com/payera-br/rivoo-agent/releases/latest/download/rivoo-agent-linux-amd64
chmod +x rivoo-agent
./rivoo-agent
```

Na primeira vez ele pergunta o jogo, o IP e a porta do RCON e a senha, e mostra um **código de
pareamento**. No painel da Rivoo: **Entrega in-game → Conectar servidor → Instalar o agente**, digite o
código. Pronto: a configuração fica salva em `rivoo-agent.json`, ao lado do binário.

Para manter rodando, use um serviço (systemd):

```ini
# /etc/systemd/system/rivoo-agent.service
[Unit]
Description=Agente Rivoo
After=network-online.target

[Service]
ExecStart=/opt/rivoo/rivoo-agent
WorkingDirectory=/opt/rivoo
Restart=always
User=rivoo

[Install]
WantedBy=multi-user.target
```

Pareie uma vez no terminal (`/opt/rivoo/rivoo-agent`), depois `systemctl enable --now rivoo-agent`.

**Windows**: baixe
[`rivoo-agent-windows-amd64.exe`](https://github.com/payera-br/rivoo-agent/releases/latest/download/rivoo-agent-windows-amd64.exe)
e abra. Mesmo passo a passo.

**Docker**

```bash
docker run -d --name rivoo-agent --restart unless-stopped --network host \
  -v rivoo-agent:/data \
  -e RIVOO_GAME=RUST -e RCON_HOST=127.0.0.1 -e RCON_PORT=28016 -e RCON_PASSWORD='<senha>' \
  ghcr.io/payera-br/rivoo-agent:latest
docker logs -f rivoo-agent   # o código de pareamento aparece aqui
```

O volume `/data` guarda a chave: sem ele, recriar o contêiner pede pareamento de novo. A imagem não
se atualiza sozinha; atualize puxando a tag nova.

**Pterodactyl**: importe [`pterodactyl/egg-rivoo-agent.json`](pterodactyl/egg-rivoo-agent.json) em
*Admin → Nests*, crie um servidor com ele na mesma máquina do jogo e preencha jogo, IP, porta e
senha do RCON. O código de pareamento aparece no console.

## Configuração

| Variável | Para quê | Padrão |
|---|---|---|
| `RIVOO_GAME` | `MINECRAFT_JAVA`, `RUST`, `PALWORLD`, `SEVEN_DAYS`, `ARK_SE`, `ARK_SA`, `CONAN`, `HYTALE`, `SOURCE_GENERIC` | pergunta |
| `RCON_HOST` | IP do console | `127.0.0.1` |
| `RCON_PORT` | porta do console | a do jogo |
| `RCON_PASSWORD` | senha do console | pergunta |
| `RIVOO_CONFIG` | caminho do arquivo de configuração | `rivoo-agent.json` ao lado do binário |
| `RIVOO_AUTO_UPDATE` | `false` desliga a atualização automática | ligada (desligada na imagem Docker) |

Variáveis valem sobre o arquivo. O arquivo é gravado só para o dono (`0600`): ele guarda a chave da
conexão e a senha do RCON.

## Comandos nos pacotes

No painel, cada pacote roda comandos com variáveis do pedido: `{player}` (nick), `{id}` (SteamID64
ou identificador do jogo), `{uuid}`, entre outras. No Conan Exiles, use `{index}`: o agente troca
pelo índice do jogador na sessão (`con {index} spawnitem 10001 5`).

Use **Testar comando** no painel para rodar um comando na hora e ver a resposta do jogo.

## Problemas

| No painel | O que fazer |
|---|---|
| "senha do RCON recusada" | confira a senha no jogo e no agente (`rivoo-agent.json` ou `RCON_PASSWORD`) |
| "não foi possível falar com o RCON" | servidor desligado, RCON desligado, IP ou porta errados, firewall |
| "o servidor não respondeu: o comando pode ter rodado" | o jogo travou no meio do comando; confira no jogo antes de reenviar |
| entrega parada em "aguardando" | o jogador não está online, ou o nick/ID da compra não bate com o do jogo |
| "a Rivoo recusou a chave" (no log) | a conexão foi excluída ou a chave trocada: apague `rivoo-agent.json` e pareie de novo |

## Desenvolvimento

Sem Go instalado, tudo roda na imagem `golang:1.25`:

```bash
make test     # go test ./...
make vet
make build    # dist/rivoo-agent (VERSION=x.y.z make build)
```

Pacotes:

- `internal/api`: protocolo `/v1/agent` da Rivoo (o mesmo do plugin Minecraft).
- `internal/rcon`: Source RCON, WebRCON (Rust) e Telnet (7 Days to Die).
- `internal/games`: por jogo, lista de jogadores, respostas de erro e preparação do comando.
- `internal/runner`: o laço de entrega (online → fila → console → confirmação).
- `internal/cloud`: o worker que a Rivoo roda para os agentes na nuvem (`rivoo-agent cloud`).
- `internal/update`: atualização assinada (ed25519).

Teste ponta a ponta com a API local: [`test/e2e/README.md`](test/e2e/README.md).

### Garantias que o código mantém

- Um comando que saiu e não teve resposta é reportado como falha ("pode ter rodado") e **nunca roda
  de novo sozinho**.
- O que rodou é sempre confirmado antes de o agente sair, atualizar ou trocar de configuração.
- Nunca dois laços na mesma fila (o worker da nuvem espera o antigo terminar).
- A senha do RCON e a chave não vão para log, erro nem heartbeat.

## Lançar uma versão

1. `main` verde no CI.
2. A API em `https://api.rivoo.gg/health` precisa responder `{"status":"ok"}`: o release confere e
   para se não responder (o binário manda a chave para esse endereço).
3. `git tag v0.1.0 && git push origin v0.1.0`.

O workflow `release` testa, compila Linux (amd64, arm64) e Windows, publica os binários, o
`checksums.txt` e o manifesto `latest.json` assinado (segredo `AGENT_SIGNING_KEY`), e a imagem
`ghcr.io/payera-br/rivoo-agent`. Tag com sufixo (`v0.2.0-rc1`) vira pré-release: não é "latest", não
chega aos agentes nem ao egg.

A chave privada de assinatura fica fora do repositório (segredo do CI e cópia guardada pelo time). A
pública está em `cmd/rivoo-agent/update.go`. Para trocar a chave: `go run ./scripts/sign keygen`,
atualize o segredo e a constante, e lance uma versão assinada com a chave **antiga** contendo a nova
pública antes de passar a assinar com a nova.
