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
	"sync"
	"time"
)

const (
	WSMagic      = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	SocketBuffer = 524288 // 512KB Buffer OS level kernel
	ChunkSize    = 131072 // 128KB Ukuran Pipa Data
)

// 🔄 ZERO-JITTER BUFFER POOL: Mengadopsi sistem daur ulang memori raksasa 128KB
var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, ChunkSize)
	},
}

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

	log.Printf("[WS Engine] Fixed Core Anti-Timeout & Extreme Upload Active on Port %s", wsPort)

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
		_ = tcpConn.SetKeepAlivePeriod(15 * time.Second)
		
		// Boost Buffer Kernel untuk kelancaran bandwidth jumbo
		_ = tcpConn.SetReadBuffer(SocketBuffer)
		_ = tcpConn.SetWriteBuffer(SocketBuffer)
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

	sshConn, err := net.DialTimeout("tcp", sshTarget, 4*time.Second)
	if err != nil {
		return
	}
	tweakSocket(sshConn)
	defer sshConn.Close()

	done := make(chan struct{}, 2)

	// --- 🚀 JALUR A: HP -> DROPBEAR SSH (FIXED ANTI-TIMEOUT UPLOAD) ---
	go func() {
		defer func() { done <- struct{}{} }()
		
		buf := bufferPool.Get().([]byte)
		defer bufferPool.Put(buf)
		
		firstPacket := true
		var totalRead int

		for {
			// Cegah timeout mati saat speedtest dengan tidak mengunci deadline terlalu sempit
			_ = client.SetReadDeadline(time.Now().Add(60 * time.Second))

			n, err := client.Read(buf)
			if err != nil || n == 0 {
				return
			}

			data := buf[:n]
			totalRead += n

			if firstPacket {
				// Deteksi Banner SSH murni
				if idx := bytes.Index(data, []byte("SSH-")); idx != -1 {
					data = data[idx:]
					firstPacket = false 
				} else if idx := bytes.Index(data, []byte{0x53, 0x53, 0x48}); idx != -1 {
					data = data[idx:]
					firstPacket = false
				} else if totalRead > 8192 {
					// AMAN: Jika data masuk sudah > 8KB tapi banner belum ketemu, matikan filter!
					// Ini menandakan koneksi bypass/upload sedang berjalan penuh.
					firstPacket = false
				} else {
					// Bakar sampah payload teks HTTP di awal koneksi
					continue
				}
			}

			_, wErr := sshConn.Write(data)
			if wErr != nil {
				return
			}

			// JIKA HANDSHAKE SUDAH SELESAI -> BREAK LOOP DAN BYPASS KE SYSTEM CALL
			if !firstPacket {
				break
			}
		}

		// Kirim sisa data upload raksasa lu langsung menggunakan io.CopyBuffer bawaan Go
		// Memakai memori pool 128KB biar hardware langsung pasok data tanpa interupsi
		uploadPool := bufferPool.Get().([]byte)
		defer bufferPool.Put(uploadPool)
		_, _ = io.CopyBuffer(sshConn, client, uploadPool)
	}()

	// --- 🚀 JALUR B: DROPBEAR SSH -> HP (DOWNLOAD MODE SPEED ULTRA) ---
	go func() {
		defer func() { done <- struct{}{} }()
		_ = sshConn.SetReadDeadline(time.Now().Add(60 * time.Second))
		
		downloadPool := bufferPool.Get().([]byte)
		defer bufferPool.Put(downloadPool)
		
		_, _ = io.CopyBuffer(client, sshConn, downloadPool)
	}()

	<-done
}
