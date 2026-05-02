#!/bin/bash

set -e

CERT_DIR="certs"
DAYS_VALID=365

# Create certs directory
echo "Creating certificate directory..."
mkdir -p $CERT_DIR

# Generate CA private key
echo "Generating CA private key..."
openssl genrsa -out $CERT_DIR/ca.key 4096

# Generate CA certificate
echo "Generating CA certificate..."
openssl req -new -x509 -key $CERT_DIR/ca.key -sha256 -days $DAYS_VALID -out $CERT_DIR/ca.crt \
  -subj "/C=US/ST=State/L=City/O=SoundMap/OU=Security/CN=SoundMap Root CA"

# Generate server private key
echo "Generating server private key..."
openssl genrsa -out $CERT_DIR/server.key 4096

# Generate server CSR
echo "Generating server certificate signing request..."
openssl req -new -key $CERT_DIR/server.key -out $CERT_DIR/server.csr \
  -subj "/C=US/ST=State/L=City/O=SoundMap/OU=Services/CN=localhost"

# Generate server certificate signed by CA
echo "Signing server certificate with CA..."
openssl x509 -req -in $CERT_DIR/server.csr -CA $CERT_DIR/ca.crt -CAkey $CERT_DIR/ca.key \
  -CAcreateserial -out $CERT_DIR/server.crt -days $DAYS_VALID -sha256 \
  -extfile <(cat <<EOF
subjectAltName = @alt_names

[alt_names]
DNS.1 = localhost
DNS.2 = *.soundmap.local
IP.1 = 127.0.0.1
IP.2 = ::1
EOF
)

# Generate client private key
echo "Generating client private key..."
openssl genrsa -out $CERT_DIR/client.key 4096

# Generate client CSR
echo "Generating client certificate signing request..."
openssl req -new -key $CERT_DIR/client.key -out $CERT_DIR/client.csr \
  -subj "/C=US/ST=State/L=City/O=SoundMap/OU=Clients/CN=soundmap-client"

# Generate client certificate signed by CA
echo "Signing client certificate with CA..."
openssl x509 -req -in $CERT_DIR/client.csr -CA $CERT_DIR/ca.crt -CAkey $CERT_DIR/ca.key \
  -CAcreateserial -out $CERT_DIR/client.crt -days $DAYS_VALID -sha256

# Clean up CSR files
rm $CERT_DIR/server.csr $CERT_DIR/client.csr

# Set proper permissions
chmod 600 $CERT_DIR/*.key
chmod 644 $CERT_DIR/*.crt

echo ""
echo "========================================"
echo "Certificate generation complete!"
echo "========================================"
echo ""
echo "Files generated in: $CERT_DIR/"
echo "  - ca.crt         : Root CA certificate (distribute to clients)"
echo "  - ca.key         : Root CA private key (keep secure)"
echo "  - server.crt     : Server certificate"
echo "  - server.key     : Server private key"
echo "  - client.crt     : Client certificate"
echo "  - client.key     : Client private key"
echo ""
echo "Valid for: $DAYS_VALID days"
echo "========================================"
