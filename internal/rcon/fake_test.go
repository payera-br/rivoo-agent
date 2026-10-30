package rcon

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

// fakeSource is a Source RCON server: password "pw"; "big" answers 10000 bytes split in packets.
func fakeSource(t *testing.T) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				write := func(id, typ int32, body string) {
					b := make([]byte, 14+len(body))
					binary.LittleEndian.PutUint32(b[0:], uint32(10+len(body)))
					binary.LittleEndian.PutUint32(b[4:], uint32(id))
					binary.LittleEndian.PutUint32(b[8:], uint32(typ))
					copy(b[12:], body)
					c.Write(b)
				}
				for {
					var size int32
					if binary.Read(c, binary.LittleEndian, &size) != nil {
						return
					}
					buf := make([]byte, size)
					if _, err := io.ReadFull(c, buf); err != nil {
						return
					}
					id := int32(binary.LittleEndian.Uint32(buf[0:]))
					typ := int32(binary.LittleEndian.Uint32(buf[4:]))
					body := strings.TrimRight(string(buf[8:]), "\x00")
					switch {
					case typ == 3 && body == "pw":
						write(id, 2, "")
					case typ == 3:
						write(-1, 2, "")
					case body == "big":
						s := strings.Repeat("x", 10000)
						write(id, 0, s[:4096])
						write(id, 0, s[4096:8192])
						write(id, 0, s[8192:])
					default:
						write(id, 0, "ran: "+body)
					}
				}
			}(c)
		}
	}()
	a := ln.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}

// fakeWebRcon answers each message with its Identifier, after first sending a log line (Identifier 0).
func fakeWebRcon(t *testing.T) (host string, port int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pw" {
			w.WriteHeader(401)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			_, b, err := c.Read(r.Context())
			if err != nil {
				return
			}
			var m struct {
				Identifier int
				Message    string
			}
			json.Unmarshal(b, &m)
			log, _ := json.Marshal(map[string]any{"Identifier": 0, "Message": "[log] something", "Type": "Generic"})
			c.Write(r.Context(), websocket.MessageText, log)
			out, _ := json.Marshal(map[string]any{"Identifier": m.Identifier, "Message": "ran: " + m.Message, "Type": "Generic"})
			c.Write(r.Context(), websocket.MessageText, out)
		}
	}))
	t.Cleanup(srv.Close)
	a := srv.Listener.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}

// fakeTelnet is a 7 Days to Die console: asks the password, then echoes commands.
func fakeTelnet(t *testing.T) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				c.Write([]byte("Please enter password:\r\n"))
				pw, _ := r.ReadString('\n')
				if strings.TrimSpace(pw) != "pw" {
					c.Write([]byte("Password incorrect, please enter password:\r\n"))
					return
				}
				c.Write([]byte("Logon successful.\r\n\r\n*** Connected with 7DTD server.\r\n"))
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					c.Write([]byte("ran: " + strings.TrimSpace(line) + "\r\n"))
				}
			}(c)
		}
	}()
	a := ln.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}
