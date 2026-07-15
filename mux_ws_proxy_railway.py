#!/usr/bin/env python3
"""
Combined TCP Multiplexer & WebSocket-SSH Proxy (Dropbear Premium Matcher).
Optimized for Railway.app & High-Throughput Performance.

[FUNGSI 1 - MUX]: 
Mendengarkan di port publik utama (PORT/443). Mengintip byte pertama:
  - 0x16 (TLS) -> Diteruskan ke Stunnel Backend.
  - Selain itu  -> Diteruskan secara internal ke WS Proxy (Local Engine).

[FUNGSI 2 - WS PROXY]:
Menerima operan non-TLS, melakukan high-speed parsing untuk upgrade WebSocket,
dan menyaring payload enhanced PATCH/POST sampah operator agar aman masuk ke Dropbear SSH.

[TUNING PERFORMA RAILWAY]:
- TCP_NODELAY untuk mematikan Nagle's Algorithm (Koneksi instan tanpa delay buffering).
- Aggressive TCP Keepalive level aplikasi agar koneksi tidak diputus sepihak oleh load balancer Railway.
- Kebal payload jumbo (limit=8192) untuk menangkal serangan 'too long line' / payload modifikasi.
- Penyetelan Buffer Ukuran Besar (64KB) untuk throughput bandwidth maksimal.
"""

import asyncio
import base64
import hashlib
import logging
import os
import signal
import sys
import secrets
import socket

# --- KONFIGURASI GLOBAL ---
WS_MAGIC = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
LISTEN_HOST = "0.0.0.0"

# 1. Port Publik Utama (Mux) - Mendukung deteksi port dinamis dari Railway
MAIN_MUX_PORT = int(os.environ.get("MAIN_MUX_PORT", os.environ.get("PORT", "443")))

# 2. Target Backend SSL/Stunnel
SSL_TARGET_HOST = os.environ.get("SSL_TARGET_HOST", "127.0.0.1")
SSL_TARGET_PORT = int(os.environ.get("SSL_TARGET_PORT", "2443"))

# 3. WS Proxy Internal (Mux oper ke sini)
WS_INTERNAL_HOST = "127.0.0.1"
WS_INTERNAL_PORT = int(os.environ.get("WS_PORT", "8880"))

# 4. Target Akhir SSH / Dropbear
SSH_TARGET_HOST = os.environ.get("WS_TARGET_HOST", "127.0.0.1")
SSH_TARGET_PORT = int(os.environ.get("WS_TARGET_PORT", "22"))

DEFAULT_RESPONSE = os.environ.get(
    "WS_RESPONSE",
    "HTTP/1.1 101 Switching Protocols\r\n\r\n",
)

TLS_HANDSHAKE_BYTE = 0x16
BUFFER_SIZE = 65536  # 64KB Buffer untuk performa throughput maksimal

# --- LOGGING SETUP ---
logging.basicConfig(
    level=logging.INFO,
    format="[railway-premium] %(asctime)s %(levelname)s %(message)s",
)
log = logging.getLogger("combined-proxy")


# ==============================================================================
# UTALITAS & PARSER CORE (HIGH-SPEED)
# ==============================================================================
def parse_headers(raw: bytes) -> dict:
    """Fungsi pembaca header kecepatan tinggi (High-Speed Engine)."""
    headers = {}
    try:
        header_part = raw.split(b"\r\n\r\n", 1)[0]
        lines = header_part.decode(errors="ignore").split("\r\n")
        for line in lines[1:]:
            if not line:
                continue
            if ":" in line:
                k, v = line.split(":", 1)
                headers[k.strip().lower()] = v.strip()
    except Exception as e:
        log.debug("Gagal parse header: %s", e)
    return headers


def make_accept_key(ws_key: str) -> str:
    sha1 = hashlib.sha1((ws_key + WS_MAGIC).encode()).digest()
    return base64.b64encode(sha1).decode()


def configure_socket_tweak(writer_spec):
    """
    Tuning Ekstrem Level Socket Khusus Lingkungan Cloud/Railway:
    1. Mengaktifkan TCP_NODELAY (Bypass Nagle Algorithm) untuk transfer instan tanpa latency.
    2. Mengaktifkan TCP Keepalive Agresif agar link tidak hang / kena pruning firewall.
    """
    sock = writer_spec.get_extra_info('socket')
    if sock is not None:
        # Tweak 1: Matikan Nagle's Algorithm -> Mengurangi latency drop packet secara drastis
        try:
            sock.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
        except Exception:
            pass

        # Tweak 2: Aktifkan Keepalive Dasar
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_KEEPALIVE, 1)
        
        # Tweak 3: Keepalive Agresif (Deteksi putus dalam hitungan detik)
        try:
            sock.setsockopt(socket.IPPROTO_TCP, socket.TCP_KEEPIDLE, 10)   # Cek idle setelah 10 detik
            sock.setsockopt(socket.IPPROTO_TCP, socket.TCP_KEEPINTVL, 3)   # Interval pengulangan 3 detik jika loss
            sock.setsockopt(socket.IPPROTO_TCP, socket.TCP_KEEPCNT, 3)     # Drop total jika 3x gagal berturut-turut
        except AttributeError:
            pass


async def pipe_generic(src: asyncio.StreamReader, dst: asyncio.StreamWriter):
    """Pipe data dua arah berkecepatan tinggi dengan alokasi buffer optimum."""
    try:
        while True:
            data = await src.read(BUFFER_SIZE)
            if not data:
                break
            dst.write(data)
            await dst.drain()
    except (ConnectionResetError, asyncio.IncompleteReadError):
        pass
    except Exception as e:
        log.debug("pipe_generic error: %s", e)
    finally:
        try:
            dst.close()
        except Exception:
            pass


# ==============================================================================
# ENGINE WS PROXY INTERNAL (SINKRONISASI SCRIPT 1)
# ==============================================================================
async def handle_ws_internal(reader: asyncio.StreamReader, writer: asyncio.StreamWriter):
    peer = writer.get_extra_info("peername")
    try:
        raw_headers = await reader.read(4096)
        if not raw_headers:
            writer.close()
            return

        headers = parse_headers(raw_headers)
        raw_text_lower = raw_headers.decode(errors="ignore").lower()

        is_ws_upgrade = "upgrade: websocket" in raw_text_lower or headers.get("upgrade", "").lower() == "websocket"

        if is_ws_upgrade:
            ws_key = headers.get("sec-websocket-key")
            if not ws_key and "sec-websocket-key:" in raw_text_lower:
                try:
                    for line in raw_headers.decode(errors="ignore").split("\r\n"):
                        if "sec-websocket-key" in line.lower():
                            ws_key = line.split(":", 1)[1].strip()
                            break
                except Exception:
                    pass

            if not ws_key:
                ws_key = base64.b64encode(secrets.token_bytes(16)).decode()

            accept_key = make_accept_key(ws_key)
            response = (
                "HTTP/1.1 101 Switching Protocols\r\n"
                "Upgrade: websocket\r\n"
                "Connection: Upgrade\r\n"
                f"Sec-WebSocket-Accept: {accept_key}\r\n"
            )
            if "sec-websocket-protocol" in headers:
                response += f"Sec-WebSocket-Protocol: {headers['sec-websocket-protocol']}\r\n"
            response += "\r\n"
            writer.write(response.encode())
        else:
            writer.write(DEFAULT_RESPONSE.encode())

        await writer.drain()

        try:
            target_reader, target_writer = await asyncio.open_connection(SSH_TARGET_HOST, SSH_TARGET_PORT)
            configure_socket_tweak(target_writer)  # Tweak koneksi arah ke SSH Backend
        except Exception as e:
            log.error("[ws-engine] Gagal konek ke SSH %s:%s -> %s", SSH_TARGET_HOST, SSH_TARGET_PORT, e)
            writer.close()
            return

        # --- TUNING DROPBEAR FILTER (Pemburu banner SSH) ---
        async def pipe_client_to_ssh(src: asyncio.StreamReader, dst: asyncio.StreamWriter):
            first_packet = True
            try:
                while True:
                    data = await src.read(BUFFER_SIZE)
                    if not data:
                        break
                    if first_packet:
                        if b"SSH-" in data:
                            idx = data.find(b"SSH-")
                            data = data[idx:]
                            first_packet = False
                        else:
                            log.info("[ws-engine] Menyaring enhanced payload (sampah operator)...")
                            continue
                    dst.write(data)
                    await dst.drain()
            except (ConnectionResetError, asyncio.IncompleteReadError):
                pass
            except Exception as e:
                log.debug("pipe_client error: %s", e)
            finally:
                try:
                    dst.close()
                except Exception:
                    pass

        await asyncio.gather(
            pipe_client_to_ssh(reader, target_writer),
            pipe_generic(target_reader, writer),
        )

    except Exception as e:
        log.error("[ws-engine] Error klien %s: %s", peer, e)
    finally:
        try:
            writer.close()
        except Exception:
            pass


# ==============================================================================
# ENGINE MULTIPLEXER UTAMA (SINKRONISASI SCRIPT 2)
# ==============================================================================
async def handle_mux_main(reader: asyncio.StreamReader, writer: asyncio.StreamWriter):
    peer = writer.get_extra_info("peername")
    try:
        # Intip byte pertama tanpa membuang data asli
        first_byte = await reader.read(1)
        if not first_byte:
            writer.close()
            return

        # Seleksi protokol berdasarkan byte awal
        if first_byte[0] == TLS_HANDSHAKE_BYTE:
            target_host, target_port, label = SSL_TARGET_HOST, SSL_TARGET_PORT, "SSL/Stunnel"
        else:
            target_host, target_port, label = WS_INTERNAL_HOST, WS_INTERNAL_PORT, "WS-Proxy Internal"

        log.info("[mux] Koneksi %s diidentifikasi sebagai %s -> port %s", peer, label, target_port)

        try:
            target_reader, target_writer = await asyncio.open_connection(target_host, target_port)
            configure_socket_tweak(target_writer)  # Tweak koneksi arah ke Backend tujuan
        except Exception as e:
            log.error("[mux] Gagal oper ke backend %s (%s:%s) -> %s", label, target_host, target_port, e)
            writer.close()
            return

        # Tembakkan kembali byte pertama yang tadi diintip ke target backend
        target_writer.write(first_byte)
        await target_writer.drain()

        # Jalankan estafet data dua arah
        await asyncio.gather(
            pipe_generic(reader, target_writer),
            pipe_generic(target_reader, writer),
        )

    except Exception as e:
        log.error("[mux] Error menangani klien %s: %s", peer, e)
    finally:
        try:
            writer.close()
        except Exception:
            pass


# ==============================================================================
# RUNNER CORE
# ==============================================================================
async def main():
    # Callback wrapper untuk menyuntikkan Socket Keepalive & NODELAY otomatis saat koneksi masuk
    async def mux_cb(r, w):
        configure_socket_tweak(w)
        await handle_mux_main(r, w)

    async def ws_cb(r, w):
        configure_socket_tweak(w)
        await handle_ws_internal(r, w)

    # 1. Start Server WS Proxy Internal (Hanya listen lokal)
    ws_server = await asyncio.start_server(ws_cb, WS_INTERNAL_HOST, WS_INTERNAL_PORT, limit=8192)
    log.info("[System] WS Engine Internal aktif di %s:%s", WS_INTERNAL_HOST, WS_INTERNAL_PORT)

    # 2. Start Server Mux Utama (Port Publik Luar - Sesuai env 'PORT' Railway)
    mux_server = await asyncio.start_server(mux_cb, LISTEN_HOST, MAIN_MUX_PORT, limit=8192)
    log.info("[System] Mux Utama Jalan di %s:%s -> Kebal Payload & Tweak Railway Aktif", LISTEN_HOST, MAIN_MUX_PORT)

    # Jalankan kedua server secara simultan selamanya
    async with ws_server, mux_server:
        await asyncio.gather(
            ws_server.serve_forever(),
            mux_server.serve_forever()
        )


def handle_sigterm(*_):
    sys.exit(0)


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, handle_sigterm)
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        pass
