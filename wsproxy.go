package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"log"
	"net"
	"os"
	"strings"
	"time"
)

const (
	WSMagic    = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	BufferSize = 131072 // Naik ke 128KB untuk kapasitas pipa data lebih longgar
	SocketBuffer = 262144 // 256KB untuk buffer read/write di level OS Kernel
)

func main() {
	wsPort := os.Getenv("WS_PORT")
	if wsPort == "" {
		wsPort = "8880"
	}
	sshTarget := "127.0.0.1:22"

	listener, err := net.Listen("tcp", "127.0.0.1:"+wsPort)
	if err != nil {
		log.Fatalf("[WS] Gagal listen internal: %v", err)
	}
	defer listener.Close()

	log.Printf("[WS Engine] Listen internal aktif di 127.0.0.1:%s -> Forward ke SSH: %s", wsPort, sshTarget)

	for {
		clientConn, err := listener.Accept()
		if err != nil {
			continue
		}
		go handleWS(clientConn, sshTarget)
	}
}

func tweakSocket(conn net.Conn) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)                  // Mematikan Nagle (Instant Response)
		_ = tcpConn.SetKeepAlive(true)                 
		_ = tcpConn.SetKeepAlivePeriod(10 * time.Second)
		
		# --- TWEAK PREMIUM SPEED UP ---
		_ = tcpConn.SetReadBuffer(SocketBuffer)  // Paksa OS kasih buffer baca raksasa
		_ = tcpConn.SetWriteBuffer(SocketBuffer) // Paksa OS kasih buffer tulis raksasa
	}
}

func handleWS(client net.Conn, sshTarget string) {
	tweakSocket(client)
	defer client.Close()

	headerBuf := make([]byte, 4096)
	n, err := client.Read(headerBuf)
	if err != nil || n == 0 {
		return
	}

	rawHeaders := string(headerBuf[:n])
	rawLower := strings.ToLower(rawHeaders)

	if strings.Contains(rawLower, "upgrade: websocket") || strings.Contains(rawLower, "websocket") {
		wsKey := ""
		lines := strings.Split(rawHeaders, "\r\n")
		for _, line := range lines {
			if strings.HasPrefix(strings.ToLower(line), "sec-websocket-key:") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					wsKey = strings.TrimSpace(parts[1])
				}
				break
			}
		}

		if wsKey == "" {
			wsKey = base64.StdEncoding.EncodeToString([]byte(time.Now().String()))
		}

		h := sha1.New()
		h.Write([]byte(wsKey + WSMagic))
		acceptKey := base64.StdEncoding.EncodeToString(h.Sum(nil))

		response := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + acceptKey + "\r\n\r\n"
		_, _ = client.Write([]byte(response))
	} else {
		defaultResp := os.Getenv("WS_RESPONSE")
		if defaultResp == "" {
			defaultResp = "HTTP/1.1 101 Switching Protocols\r\n\r\n"
		}
		_, _ = client.Write([]byte(defaultResp))
	}

	sshConn, err := net.DialTimeout("tcp", sshTarget, 5*time.Second)
	if err != nil {
		return
	}
	tweakSocket(sshConn)
	defer sshConn.Close()

	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		buffer := make([]byte, BufferSize)
		firstPacket := true
		var totalRead int

		for {
			n, err := client.Read(buffer)
			if n > 0 {
				data := buffer[:n]
				totalRead += n

				if firstPacket {
					if idx := bytes.Index(data, []byte("SSH-")); idx != -1 {
						data = data[idx:]
						firstPacket = false 
					} else if totalRead > 4096 {
						firstPacket = false
					} else {
						continue
					}
				}
				
				_, wErr := sshConn.Write(data)
				if wErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		buffer := make([]byte, BufferSize)
		for {
			n, err := sshConn.Read(buffer)
			if n > 0 {
				_, wErr := client.Write(buffer[:n])
				if wErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	<-done
}
