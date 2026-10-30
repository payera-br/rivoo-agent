# Fake Minecraft RCON (Source protocol) on 127.0.0.1:25599, password "pw". Steve is online.
import socket, struct, threading, sys
log = open(sys.argv[1], 'a')
def handle(c):
    def send(i, t, body):
        b = body.encode()
        c.sendall(struct.pack('<iii', len(b) + 10, i, t) + b + b'\x00\x00')
    while True:
        h = c.recv(4)
        if len(h) < 4: return
        (size,) = struct.unpack('<i', h)
        data = b''
        while len(data) < size: data += c.recv(size - len(data))
        i, t = struct.unpack('<ii', data[:8]); body = data[8:-2].decode()
        if t == 3:
            send(i if body == 'pw' else -1, 2, ''); continue
        log.write(body + '\n'); log.flush()
        if body == 'list': send(i, 0, 'There are 1 of a max of 20 players online: Steve')
        elif body.startswith('bad'): send(i, 0, 'Unknown or incomplete command, see below for error')
        else: send(i, 0, 'ran: ' + body)
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1); s.bind(('127.0.0.1', 25599)); s.listen()
while True:
    c, _ = s.accept(); threading.Thread(target=handle, args=(c,), daemon=True).start()
