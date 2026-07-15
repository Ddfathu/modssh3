package main

import (
	"io"
	"log"
	"net"
	"os"
	"time"
)

const (
	TLSHandshakeByte = 0x16
	SocketBuffer     = 524288 // DI-BOOST: 512KB Kernel Socket Buffer (Anti Mampet Pas Upload)
)

func main() {
	publicPort := os.Getenv("PORT")
	if publicPort == "" {
		publicPort = "8080"
	}

	sslTarget := os.Getenv("SSL_TARGET_HOST") + ":" + os.Getenv("SSL_TARGET_PORT")
	if sslTarget == ":" {
		sslTarget = "127.0.0.1:2443"
	}

	wsTarget := os.Getenv("WS_MUX_TARGET_HOST") + ":" + os.Getenv("WS_MUX_TARGET_PORT")
	if wsTarget == ":" {
		wsTarget = "127.0.0.1:8880"
	}

	listener, err := net.Listen("tcp", "0.0.0.0:"+publicPort)
	if err != nil {
		log.Fatalf("[Mux] Gagal listen di port %s: %v", publicPort, err)
	}
	defer listener.Close()

	log.Printf("[Mux] Jalan di 0.0.0.0:%s -> SSL:%s | WS:%s -> High-Speed Mode", publicPort, sslTarget, wsTarget)

	for {
		clientConn, err := listener.Accept()
		if err != nil {
			continue
		}
		go handleClient(clientConn, sslTarget, wsTarget)
	}
}

func tweakSocket(conn net.Conn) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true) // Matikan Nagle's Algorithm (Anti Delay / Instant Response)
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(10 * time.Second) // Keepalive agresif 10 detik
		
		// SUNTIKAN HIGH-SPEED: Paksa kernel alokasikan buffer raksasa agar speed upload losss!
		_ = tcpConn.SetReadBuffer(SocketBuffer)
		_ = tcpConn.SetWriteBuffer(SocketBuffer)
	}
}

func handleClient(client net.Conn, sslTarget, wsTarget string) {
	tweakSocket(client)
	defer client.Close()

	// Intip 1 byte pertama
	firstByte := make([]byte, 1)
	_, err := client.Read(firstByte)
	if err != nil {
		return
	}

	var targetAddr string
	var label string

	if firstByte[0] == TLSHandshakeByte {
		targetAddr = sslTarget
		label = "SSL/Stunnel"
	} else {
		targetAddr = wsTarget
		label = "WS-Proxy"
	}

	log.Printf("[Mux] Koneksi dari %s dialihkan ke %s (%s)", client.RemoteAddr(), label, targetAddr)

	backendConn, err := net.DialTimeout("tcp", targetAddr, 5*time.Second)
	if err != nil {
		log.Printf("[Mux] Gagal konek ke backend %s: %v", label, err)
		return
	}
	tweakSocket(backendConn)
	defer backendConn.Close()

	// Tembakkan kembali byte yang diintip tadi ke backend
	_, err = backendConn.Write(firstByte)
	if err != nil {
		return
	}

	// Relay data 2 arah secara simultan menggunakan io.Copy (Zero-Copy System Call)
	// Menghilangkan CPU bottleneck pas data upload speedtest diperas abis
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(backendConn, client)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, backendConn)
		done <- struct{}{}
	}()

	<-done
}
