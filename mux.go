package main

import (
	"log"
	"net"
	"os"
	"time"
)

const (
	TLSHandshakeByte = 0x16
	BufferSize       = 65536 // 64KB Buffer untuk speed ngacir
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

	log.Printf("[Mux] Jalan di 0.0.0.0:%s -> SSL:%s | WS:%s", publicPort, sslTarget, wsTarget)

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
		_ = tcpConn.SetNoDelay(true) // Matikan Nagle's Algorithm (Anti Delay)
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(10 * time.Second) // Keepalive agresif 10 detik
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

	// Relay data 2 arah secara simultan
	done := make(chan struct{}, 2)
	go pipe(client, backendConn, done)
	go pipe(backendConn, client, done)

	<-done
}

func pipe(src, dst net.Conn, done chan struct{}) {
	buffer := make([]byte, BufferSize)
	for {
		n, err := src.Read(buffer)
		if n > 0 {
			_, wErr := dst.Write(buffer[:n])
			if wErr != nil {
				break
			}
		}
		if err != nil {
			break
		}
	}
	done <- struct{}{}
}
