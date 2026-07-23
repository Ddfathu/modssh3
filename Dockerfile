# Stage 1: Hanya untuk build Go Binary (Mux & Wsproxy) agar tetap ringan
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY mux.go .
COPY wsproxy.go .
RUN go build -ldflags="-s -w" -o /usr/local/bin/mux mux.go
RUN go build -ldflags="-s -w" -o /usr/local/bin/wsproxy wsproxy.go

# Stage 2: Runner Utama menggunakan Ubuntu 22.04
FROM ubuntu:22.04
ENV DEBIAN_FRONTEND=noninteractive

# Install dependencies bawaan + tools buat compile BadVPN langsung di Ubuntu
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
    && rm -rf /var/lib/apt/lists/*

# Ambil binary Go dari Stage 1
COPY --from=builder /usr/local/bin/mux /usr/local/bin/mux
COPY --from=builder /usr/local/bin/wsproxy /usr/local/bin/wsproxy

# COMPILE BADVPN LANGSUNG DI UBUNTU (Biar klop & gak korup)
WORKDIR /src
RUN curl -fsSL https://github.com/ambrop72/badvpn/archive/refs/tags/1.999.130.tar.gz | tar -xz \
    && cd badvpn-1.999.130 \
    && mkdir build && cd build \
    && cmake .. -DBUILD_NOTHING_BY_DEFAULT=1 -DBUILD_UDPGW=1 \
    && make badvpn-udpgw \
    && cp udpgw/badvpn-udpgw /usr/local/bin/badvpn-udpgw \
    && cd / && rm -rf /src

# Pindah kembali ke folder utama app
WORKDIR /app

# Install cloudflared
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
