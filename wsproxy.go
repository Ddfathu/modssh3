package main

import (
	"crypto/sha1"
	"encoding/base64"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"time"
)

const (
	WSMagic      = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	SocketBuffer = 524288 // DI-BOOST: 512KB Buffer Kernel biar pipa bandwidth super longgar
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

	log.Printf("[WS Engine] Extreme Speed Mode Aktif di 127.0.0.1:%s", wsPort)

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
		_ = tcpConn.SetNoDelay(true) // Instant transmission
		_ = tcpConn.SetKeepAlive(true)                 
		_ = tcpConn.SetKeepAlivePeriod(10 * time.Second)
		
		// Paksa Kernel mengalokasikan memori buffer raksasa untuk speedtest
		_ = tcpConn.SetReadBuffer(SocketBuffer)  
		_ = tcpConn.SetWriteBuffer(SocketBuffer) 
	}
}

func handleWS(client net.Conn, sshTarget string) {
	tweakSocket(client)
	defer client.Close()

	// Baca HTTP Header awal (maksimal 4096 byte)
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

	// Konek ke Dropbear SSH Backend
	sshConn, err := net.DialTimeout("tcp", sshTarget, 4*time.Second)
	if err != nil {
		return
	}
	tweakSocket(sshConn)
	defer sshConn.Close()

	done := make(chan struct{}, 2)

	// --- ULTRA BYPASS ENGINE (ANTI-LEMOTE & ANTI-REKONEK) ---
	go func() {
		defer func() { done <- struct{}{} }()
		
		// Paket Pertama: Biasanya berisi payload injector manipulasi HTTP
		buf1 := make([]byte, 32768)
		n1, err := client.Read(buf1)
		if err != nil || n1 == 0 {
			return
		}

		// Cari posisi aman text banner SSH dari payload pembuka
		dataPayload := buf1[:n1]
		for i := 0; i < len(dataPayload)-4; i++ {
			if dataPayload[i] == 'S' && dataPayload[i+1] == 'S' && dataPayload[i+2] == 'H' && dataPayload[i+3] == '-' {
				dataPayload = dataPayload[i:]
				break
			}
		}

		// Kirim data bersih awal ke Dropbear
		_, err = sshConn.Write(dataPayload)
		if err != nil {
			return
		}

		// SETELAH PAKET 1 SELESAI, BYPASS TOTAL TANPA FILTER SAMA SEKALI!
		// Menggunakan io.Copy agar data dilempar langsung via kernel space (Speed Murni Tanpa Hambatan)
		_, _ = io.Copy(sshConn, client)
	}()

	// Jalur Arah Sebaliknya (SSH -> Client) - Full Speed Bypass
	go func() {
		defer func() { done <- struct{}{} }()
		_, _ = io.Copy(client, sshConn)
	}()

	<-done
}
