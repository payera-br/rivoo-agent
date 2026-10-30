# rivoo-agent

Entrega as compras da Rivoo no servidor de jogo por RCON. Roda ao lado do servidor (ou em qualquer
máquina que alcance o RCON), busca a fila de comandos na Rivoo e executa só com o jogador online.

Jogos: Minecraft Java (Geyser), Rust (WebRCON), Palworld, 7 Days to Die (Telnet); ARK, Conan e
Hytale (via HyRCON) em beta.

## Desenvolvimento

Sem Go instalado: `make test`, `make vet` e `make build` rodam na imagem `golang:1.25`.
