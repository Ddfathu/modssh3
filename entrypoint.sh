#!/bin/bash

# ==============================================================================
# TWEAK KINERJA SISTEM (RAILWAY HIGH-THROUGHPUT)
# ==============================================================================
# Naikkan batas maksimum open file descriptors agar kuat menampung ribuan koneksi serentak
ulimit -n 65535

# Tweak Network Stack untuk mempercepat daur ulang port/socket mati (anti-stuck)
sysctl -w net.ipv4.tcp_fin_timeout=15 2>/dev/null
sysctl -w net.ipv4.tcp_tw_reuse=1 2>/dev/null
sysctl -w net.core.somaxconn=4096 2>/dev/null

# ==============================================================================
# KONFIGURASI ENV & PORT
# ==============================================================================
USER_NAME="${SSH_USER:-dd}"
USER_PASS="${SSH_PASSWORD:-dd}"

# Port PUBLIK Utama Railway
PUBLIC_PORT="${PORT:-8080}"

# Port INTERNAL (Dipakai komunikasi antar-proses di dalam container)
SSL_INTERNAL_PORT="${SSL_INTERNAL_PORT:-2443}"
WS_INTERNAL_PORT="${WS_INTERNAL_PORT:-8880}"

echo "[*] Mengonfigurasi Server Message Dropbear (Banner Pra-Login)..."
cat << 'EOF' > /etc/dropbear_banner
=================================================
                  SELAMAT MENIKMATI
             PREMIUM SSH SERVER DROPBEAR modssh3        
=================================================
       Dilarang Torrent / DDOS / Hacking! 
                 Powered By: dedefathu
=================================================
EOF

echo "[*] Mengonfigurasi Respon Server (Pasca-Login)..."
cat << 'EOF' > /etc/profile.d/99-respon-server.sh
#!/bin/bash
clear
echo -e "\e[1;36m=================================================\e[0m"
echo -e "\e[1;32m       [✓] BERHASIL TERHUBUNG KE SERVER!         \e[0m"
echo -e "\e[1;36m=================================================\e[0m"
echo -e "\e[1;37m Username     : \e[1;33m$USER\e[0m"
echo -e "\e[1;37m Waktu Server : \e[1;33m$(date)\e[0m"
echo -e "\e[1;37m OS           : \e[1;33mUbuntu 22.04 (Dropbear Mode)\e[0m"
echo -e "\e[1;36m=================================================\e[0m"
echo -e "\e[1;31m   TETAP PATUHI RULES SERVER AGAR TIDAK BANNED   \e[0m"
echo -e "\e[1;36m=================================================\e[0m"
EOF
chmod +x /etc/profile.d/99-respon-server.sh

echo "[*] Mengonfigurasi User SSH..."
if ! id "$USER_NAME" &>/dev/null; then
    useradd -m -s /bin/bash "$USER_NAME"
    usermod -aG sudo "$USER_NAME"
fi
echo "$USER_NAME:$USER_PASS" | chpasswd

echo "[*] Memulai Dropbear Server di Port Lokal 22..."
# -W 65536 = Memaksimalkan buffer size Dropbear agar speed download ngacir
/usr/sbin/dropbear -p 127.0.0.1:22 -b /etc/dropbear_banner -W 65536

echo "[*] Membuat konfigurasi Stunnel (internal) di Port $SSL_INTERNAL_PORT..."
cat <<EOF > /etc/stunnel/stunnel.conf
pid = /var/run/stunnel.pid
foreground = yes
debug = 4

[ssh-ssl]
accept = 127.0.0.1:$SSL_INTERNAL_PORT
connect = 127.0.0.1:22
cert = /etc/stunnel/stunnel.pem
EOF

# --- Catatan: Akses menu otomatis di .bashrc sudah dihapus agar bersih dari error ---
echo "[*] Menambahkan sesuatu di .bashrc..."
cat <<'EOF'>> ~/.bashrc
clear
R='\e[1;31m'
G='\e[1;32m'
C='\e[1;36m'
N='\e[0m'

alias c='clear'
alias x='exit'
alias +x='chmod +x'
alias cls='clear;ls'
EOF

echo "[*] Memulai Stunnel (internal, port $SSL_INTERNAL_PORT)..."
stunnel /etc/stunnel/stunnel.conf &

# --- Argo Tunnel (cloudflared), jalur tambahan khusus WS ---
if [ -n "$CF_TUNNEL_TOKEN" ]; then
    echo "[*] Menjalankan Cloudflare Tunnel (Argo) via token..."
    cloudflared tunnel run --token "$CF_TUNNEL_TOKEN" &
else
    echo "[!] CF_TUNNEL_TOKEN tidak diset -> Cloudflare Tunnel dilewati."
fi

echo "[*] Memulai WebSocket Proxy (Golang Engine internal) di Port $WS_INTERNAL_PORT..."
# Dijalankan di background (&) agar script terus berjalan ke bawah
WS_PORT="$WS_INTERNAL_PORT" /usr/local/bin/wsproxy &

echo "[*] Memulai Multiplexer Publik (Golang Engine) di Port PUBLIK $PUBLIC_PORT..."
# Menggunakan exec untuk mengunci proses utama kontainer ke Mux publik
exec env \
    PORT="$PUBLIC_PORT" \
    SSL_TARGET_HOST="127.0.0.1" SSL_TARGET_PORT="$SSL_INTERNAL_PORT" \
    WS_MUX_TARGET_HOST="127.0.0.1" WS_MUX_TARGET_PORT="$WS_INTERNAL_PORT" \
    /usr/local/bin/mux
