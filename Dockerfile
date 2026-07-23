# Stage 1: Builder untuk Go Binary dan BadVPN UDPGW
FROM golang:1.22-alpine AS builder
RUN apk update && apk add --no-cache cmake make gcc g++ musl-dev linux-headers curl

WORKDIR /app
# Menyalin dan kompilasi langsung di dalam docker agar binary-nya super ringan
COPY mux.go .
COPY wsproxy.go .
RUN go build -ldflags="-s -w" -o /usr/local/bin/mux mux.go
RUN go build -ldflags="-s -w" -o /usr/local/bin/wsproxy wsproxy.go

# Download dan Compile BadVPN dari Source Resmi
WORKDIR /src
RUN curl -fsSL https://github.com/ambrop72/badvpn/archive/refs/tags/1.999.130.tar.gz | tar -xz \
    && cd badvpn-1.999.130 \
    && mkdir build && cd build \
    && cmake .. -DBUILD_NOTHING_BY_DEFAULT=1 -DBUILD_UDPGW=1 \
    && make badvpn-udpgw \
    && cp udpgw/badvpn-udpgw /usr/local/bin/badvpn-udpgw

# Stage 2: Runner Utama menggunakan Ubuntu 22.04
FROM ubuntu:22.04
ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y \
    dropbear \
    stunnel4 \
    openssl \
    sudo \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Ambil seluruh binary dari tahap builder (Termasuk BadVPN)
COPY --from=builder /usr/local/bin/mux /usr/local/bin/mux
COPY --from=builder /usr/local/bin/wsproxy /usr/local/bin/wsproxy
COPY --from=builder /usr/local/bin/badvpn-udpgw /usr/local/bin/badvpn-udpgw

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
