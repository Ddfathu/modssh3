FROM ubuntu:22.04
ENV DEBIAN_FRONTEND=noninteractive

# 1. Install semua dependensi Ubuntu bawaan kamu + alat compile (cmake, make, gcc)
RUN apt-get update && apt-get install -y \
    dropbear \
    stunnel4 \
    openssl \
    sudo \
    curl \
    cmake \
    make \
    gcc \
    g++ \
    git \
    && rm -rf /var/lib/apt/lists/*

# 2. Ambil installer Go sementara untuk compile Mux dan Wsproxy di lingkungan Ubuntu
RUN curl -fsSL https://go.dev/dl/go1.22.5.linux-amd64.tar.gz | tar -C /usr/local -xz
ENV PATH=$PATH:/usr/local/go/bin

WORKDIR /app
COPY mux.go .
COPY wsproxy.go .
# Compile Mux & Wsproxy langsung di lingkungan Ubuntu agar arsitekturnya klop
RUN go build -ldflags="-s -w" -o /usr/local/bin/mux mux.go
RUN go build -ldflags="-s -w" -o /usr/local/bin/wsproxy wsproxy.go

# 3. COMPILE BADVPN LANGSUNG DI UBUNTU (Biar gak bentrok / No such file)
WORKDIR /src
RUN curl -fsSL https://github.com/ambrop72/badvpn/archive/refs/tags/1.999.130.tar.gz | tar -xz \
    && cd badvpn-1.999.130 \
    && mkdir build && cd build \
    && cmake .. -DBUILD_NOTHING_BY_DEFAULT=1 -DBUILD_UDPGW=1 \
    && make badvpn-udpgw \
    && cp udpgw/badvpn-udpgw /usr/local/bin/badvpn-udpgw \
    && cd / && rm -rf /src /usr/local/go

# Pindah kembali ke folder utama app
WORKDIR /app

# 4. Install cloudflared
RUN curl -fsSL -o /usr/local/bin/cloudflared \
    https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 \
    && chmod +x /usr/local/bin/cloudflared

RUN mkdir -p /var/run/dropbear /var/run/stunnel /etc/dropbear

RUN openssl req -new -newkey rsa:2048 -days 365 -nodes -x509 \
    -subj "/C=ID/ST=Jakarta/L=Jakarta/O=RailwaySSH/CN=localhost" \
    -keyout /etc/stunnel/stunnel.pem -out /etc/stunnel/stunnel.pem

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 8080
ENTRYPOINT ["/entrypoint.sh"]
