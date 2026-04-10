#!/bin/bash
# docker/certs/generate.sh — generate a self-signed CA + server cert
# used by every cdn-sim container. ECDSA P-256 keys match the server's
# TLS config. SAN list includes every in-cluster IP that any cdn-sim
# service binds to so that TLS verification succeeds regardless of
# which hop the client dials.
#
# Idempotent: if ca.key and server.crt already exist we exit early.
# Re-running would silently overwrite certificates baked into previously
# built Docker images and cause TLS handshake failures for running
# containers. Delete the files manually to force regeneration:
#
#   rm docker/certs/*.crt docker/certs/*.key docker/certs/*.csr \
#      docker/certs/*.srl docker/certs/*.ext && bash docker/certs/generate.sh
set -euo pipefail
cd "$(dirname "$0")"

if [ -f ca.key ] && [ -f ca.crt ] && [ -f server.key ] && [ -f server.crt ]; then
    echo "Certs already exist in $(pwd); delete them manually to regenerate."
    ls -1 *.crt *.key 2>/dev/null
    exit 0
fi

# Root CA
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
    -keyout ca.key -out ca.crt -days 365 -nodes \
    -subj "/CN=CDN-Sim Test CA" 2>/dev/null

# Server key + CSR
openssl req -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
    -keyout server.key -out server.csr -nodes \
    -subj "/CN=cdn-sim" 2>/dev/null

# Note: keyEncipherment is for RSA cipher suites. ECDSA certs used with
# TLS 1.3 (which QUIC/H3 mandates) do not need it — digitalSignature
# alone is correct per RFC 5480 §3.
cat > server.ext << 'EOF'
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage = digitalSignature
extendedKeyUsage = serverAuth, clientAuth
subjectAltName = @alt_names
[alt_names]
DNS.1 = origin
DNS.2 = shield
DNS.3 = edge-sg
DNS.4 = edge-mumbai
DNS.5 = localhost
DNS.6 = cdn-sim
IP.1 = 172.20.0.10
IP.2 = 172.20.0.20
IP.3 = 172.21.0.20
IP.4 = 172.21.0.30
IP.5 = 172.21.0.40
IP.6 = 172.22.0.30
IP.7 = 172.22.0.40
IP.8 = 172.22.0.100
IP.9 = 127.0.0.1
EOF

openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key \
    -CAcreateserial -out server.crt -days 365 \
    -extfile server.ext 2>/dev/null

# Tidy intermediate files that are only needed during generation (L-6).
rm -f server.csr server.ext ca.srl

echo "Generated certs in $(pwd)"
ls -1 *.crt *.key
