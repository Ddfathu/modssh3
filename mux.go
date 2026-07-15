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

	// Gunakan Buffered Reader supaya bisa ngintip data tanpa merusak stream asli
	reader := bufio.NewReaderSize(client, 1024)

	// Batasi waktu ngintip byte pertama (Anti-Stuck / Anti-Sunek)
	// Jika dalam 3 detik client ga kirim data, otomatis anggap sebagai WS/Payload standar
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	
	// Intip 1 byte pertama tanpa membuangnya dari buffer
	firstByte, err := reader.Peek(1)
	
	// Reset kembali deadline ke normal agar koneksi tidak terputus setelah 3 detik
	_ = client.SetReadDeadline(time.Time{})

	var targetAddr string
	var label string

	// Jika timeout atau gagal baca, default dialihkan ke WS-Proxy (biasanya payload injeksi nunggu respon)
	if err != nil {
		targetAddr = wsTarget
		label = "WS-Proxy (Default/Timeout)"
	} else if firstByte[0] == TLSHandshakeByte {
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

	// PENTING: Tulis ulang data yang sudah dibaca di buffer (termasuk byte yang diintip tadi)
	// io.Copy tidak bisa dipakai langsung dari 'client' karena datanya sudah tertahan di 'reader'
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(backendConn, reader) // Mengalirkan data dari buffer reader ke backend
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, backendConn)
		done <- struct{}{}
	}()

	<-done
}
