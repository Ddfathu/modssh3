# Stage 1: Build Semuanya di Alpine (Go & BadVPN) secara Static
FROM golang:1.22-alpine AS builder
RUN apk update && apk add --no-cache cmake make gcc g++ musl-dev linux-headers curl

WORKDIR /app
COPY mux.go .
COPY wsproxy.go .
# Tambah CGO_ENABLED=0 agar binary Go bersifat standalone (static) & cocok di Alpine
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /usr/local/bin/mux mux.go
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /usr/local/bin/wsproxy wsproxy.go

WORKDIR /src
RUN curl -fsSL https://github.com/ambrop72/badvpn/archive/refs/tags/1.999.130.tar.gz | tar -xz \
    && cd badvpn-1.999.130 \
    && mkdir build && cd build \
    && cmake .. -DBUILD_NOTHING_BY_DEFAULT=1 -DBUILD_UDPGW=1 \
    && make badvpn-udpgw \
    && mkdir -p /app_bin && cp udpgw/badvpn-udpgw /app_bin/badvpn-udpgw

# Stage 2: Runner Utama Menggunakan Alpine 3.20 (Super Cepat & Ringan)
FROM alpine:3.20
ENV DEBIAN_FRONTEND=noninteractive

# Install semua kebutuhan runtime versi Alpine
RUN apk update && apk add --no-cache \
    dropbear \
    stunnel \
    openssl \
    sudo \
    curl \
    bash \
    gcompat

# Salin semua binary hasil compile Stage 1 yang sudah static dan aman
COPY --from=builder /usr/local/bin/mux /usr/local/bin/mux
COPY --from=builder /usr/local/bin/wsproxy /usr/local/bin/wsproxy
COPY --from=builder /app_bin/badvpn-udpgw /usr/local/bin/badvpn-udpgw

WORKDIR /app

RUN mkdir -p /var/run/dropbear /var/run/stunnel /etc/dropbear

RUN openssl req -new -newkey rsa:2048 -days 365 -nodes -x509 \
    -subj "/C=ID/ST=Jakarta/L=Jakarta/O=RailwaySSH/CN=localhost" \
    -keyout /etc/stunnel/stunnel.pem -out /etc/stunnel/stunnel.pem

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 8080
ENTRYPOINT ["/entrypoint.sh"]
