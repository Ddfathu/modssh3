FROM ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive

# --- MODIFIKASI: Mengganti openssh-server dengan dropbear ---
RUN apt-get update && apt-get install -y \
    dropbear \
    stunnel4 \
    openssl \
    sudo \
    python3 \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Install cloudflared (untuk Argo Tunnel, jalur WS)
RUN curl -fsSL -o /usr/local/bin/cloudflared \
    https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 \
    && chmod +x /usr/local/bin/cloudflared

# Membuat direktori run untuk dropbear dan stunnel
RUN mkdir -p /var/run/dropbear /var/run/stunnel /etc/dropbear

# Membuat satu sertifikat .pem gabungan yang valid untuk Stunnel
RUN openssl req -new -newkey rsa:2048 -days 365 -nodes -x509 \
    -subj "/C=ID/ST=Jakarta/L=Jakarta/O=RailwaySSH/CN=localhost" \
    -keyout /etc/stunnel/stunnel.pem -out /etc/stunnel/stunnel.pem

# Salin entrypoint script utama
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# --- PENTING: File menu, addssh, delssh, listssh sudah DIHAPUS TOTAL dari sini ---

# Salin script Python gabungan ter-tweak khusus Railway
COPY mux_ws_proxy_railway.py /usr/local/bin/mux_ws_proxy_railway.py
RUN chmod +x /usr/local/bin/mux_ws_proxy_railway.py

# Cukup SATU port publik: Mux internal yang membedakan SSL vs WS secara otomatis
EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
