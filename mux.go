package main

import (
	"bufio"
	"io"
	"log"
	"net"
	"os"
	"time"
)

const (
	TLSHandshakeByte = 0x16
	SocketBuffer     = 524288 // 512KB Kernel Socket Buffer
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
		_ = tcpConn.SetNoDelay(true)
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(10 * time.Second)
		_ = tcpConn.SetReadBuffer(SocketBuffer)
		_ = tcpConn.SetWriteBuffer(SocketBuffer)
	}
}

func handleClient(client net.Conn, sslTarget, wsTarget string) {
	tweakSocket(client)
	defer client.Close()

	// Perbesar buffer reader jadi 4KB supaya aman menampung payload jumbo lu saat diintip
	reader := bufio.NewReaderSize(client, 4096)

	// Batasi waktu ngintip byte pertama (5 detik agar toleran pada jaringan naik turun)
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	
	// Intip 4 byte pertama tanpa membuangnya dari buffer
	firstBytes, err := reader.Peek(4)
	
	// Reset kembali deadline ke normal agar koneksi tidak terputus
	_ = client.SetReadDeadline(time.Time{})

	var targetAddr string
	var label string

	// Pengecekan TLS yang lebih presisi
	if err == nil && len(firstBytes) > 0 && firstBytes[0] == TLSHandshakeByte {
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

	done := make(chan struct{}, 2)
	
	// PENGIRIMAN DATA DENGAN BUFFER JUMBO (ANTI-DROP PAS YOUTUBE)
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, SocketBuffer)
		_, _ = io.CopyBuffer(backendConn, reader, buf) 
	}()
	
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, SocketBuffer)
		_, _ = io.CopyBuffer(client, backendConn, buf)
	}()

	<-done
}
