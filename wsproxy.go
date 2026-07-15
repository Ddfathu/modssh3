package main

import (
	"bytes"
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
	SocketBuffer = 524288 // 512KB Buffer OS biar data upload gak antre
	ChunkBuffer  = 131072 // 128KB Pipa data internal
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

	log.Printf("[WS Engine] Mode High-Throughput Aktif di Port %s", wsPort)

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
		
		// Set buffer kernel raksasa khusus Railway
		_ = tcpConn.SetReadBuffer(SocketBuffer)
		_ = tcpConn.SetWriteBuffer(SocketBuffer)
	}
}

func handleWS(client net.Conn, sshTarget string) {
	tweakSocket(client)
	defer client.Close()

	// Baca header HTTP awal (Maksimal 4096 byte)
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

	// Dial ke Dropbear
	sshConn, err := net.DialTimeout("tcp", sshTarget, 4*time.Second)
	if err != nil {
		return
	}
	tweakSocket(sshConn)
	defer sshConn.Close()

	done := make(chan struct{}, 2)

	// --- STREAM ENGINE KENCANG & ANTI-SUNEK ---
	go func() {
		defer func() { done <- struct{}{} }()
		
		// Gunakan buffer 128KB langsung untuk menampung data pembuka HTTP Custom
		initBuf := make([]byte, ChunkBuffer)
		nData, err := client.Read(initBuf)
		if err != nil || nData == 0 {
			return
		}

		dataPayload := initBuf[:nData]
		// Kupas payload nyari banner Dropbear SSH-
		if idx := bytes.Index(dataPayload, []byte("SSH-")); idx != -1 {
			dataPayload = dataPayload[idx:]
		}

		// Kirim data pembuka yang sudah bersih
		_, err = sshConn.Write(dataPayload)
		if err != nil {
			return
		}

		// BYPASS KECEPATAN TINGGI: Gunakan CopyBuffer dengan size 128KB untuk sisa data upload
		uploadPool := make([]byte, ChunkBuffer)
		_, _ = io.CopyBuffer(sshConn, client, uploadPool)
	}()

	// Jalur Download (SSH -> Client) - Full Speed
	go func() {
		defer func() { done <- struct{}{} }()
		downloadPool := make([]byte, ChunkBuffer)
		_, _ = io.CopyBuffer(client, sshConn, downloadPool)
	}()

	<-done
}
