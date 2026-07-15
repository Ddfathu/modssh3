FROM golang:1.22-alpine AS builder
WORKDIR /app
# Menyalin dan kompilasi langsung di dalam docker agar binary-nya super ringan
COPY mux.go .
COPY wsproxy.go .
RUN go build -ldflags="-s -w" -o /usr/local/bin/mux mux.go
RUN go build -ldflags="-s -w" -o /usr/local/bin/wsproxy wsproxy.go

FROM ubuntu:22.04
ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y \
    dropbear \
    stunnel4 \
    openssl \
    sudo \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Ambil binary dari tahap builder
COPY --from=builder /usr/local/bin/mux /usr/local/bin/mux
COPY --from=builder /usr/local/bin/wsproxy /usr/local/bin/wsproxy

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

# Akses menu / addssh / delssh / listssh SUDAH DIHAPUS TOTAL dari sini

EXPOSE 8080
ENTRYPOINT ["/entrypoint.sh"]
