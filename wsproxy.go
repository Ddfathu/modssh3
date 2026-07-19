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
	BufferSize = 65536 // 64KB Buffer untuk performa maksimal
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
		_ = tcpConn.SetNoDelay(true)                  
		_ = tcpConn.SetKeepAlive(true)                 
		_ = tcpConn.SetKeepAlivePeriod(10 * time.Second) 
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

	// --- FIX FILTER: KEBAL PAYLOAD JUMBO & ANTI-REKONEK YOUTUBE ---
	go func() {
		defer func() { done <- struct{}{} }()
		buffer := make([]byte, BufferSize)
		
		// FASE 1: PENYARINGAN SAMPAH PAYLOAD (Hanya berjalan sekali di awal koneksi)
		for {
			n, err := client.Read(buffer)
			if err != nil {
				return
			}
			if n > 0 {
				data := buffer[:n]
				if idx := bytes.Index(data, []byte("SSH-")); idx != -1 {
					// Sampah payload dibuang, ambil dari "SSH-" sampai ujung paket ini
					_, wErr := sshConn.Write(data[idx:])
					if wErr != nil {
						return
					}
					break // Keluar dari Fase 1, lanjut ke Fase 2 (Mode Los)
				}
				// Jika string "SSH-" belum ketemu, berarti ini serpihan sampah payload.
				// Diabaikan saja, loop dilanjutkan untuk membaca paket data berikutnya.
			}
		}

		// FASE 2: PURE PIPING (Berjalan lancar setelah jabat tangan sukses)
		// Mode Los tanpa filter penyaringan lagi, aman 100% dari rekonek YouTube
		for {
			n, err := client.Read(buffer)
			if n > 0 {
				_, wErr := sshConn.Write(buffer[:n])
				if wErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Pipe arah sebaliknya (SSH/Dropbear -> Client) - Full Loss tanpa filter
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
