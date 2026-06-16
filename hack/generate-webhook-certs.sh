#!/bin/bash
# Generate self-signed webhook certificates for local testing
set -e

NAMESPACE=${1:-local-testing}
SERVICE_NAME=${2:-webhook-service}
SECRET_NAME=${3:-webhook-server-cert}
KUBECTL_BIN=${KUBECTL:-kubectl}

echo "Generating webhook certificates for namespace: $NAMESPACE, service: $SERVICE_NAME"

# Create temp directory for certificates
CERT_DIR=$(mktemp -d)
trap "rm -rf $CERT_DIR" EXIT

cd "$CERT_DIR"

# Generate CA private key
openssl genrsa -out ca.key 2048

# Generate CA certificate
openssl req -new -x509 -days 365 -key ca.key -out ca.crt \
  -subj "/CN=cupboard-webhook-ca"

# Generate server private key
openssl genrsa -out tls.key 2048

# Create config for server certificate with SAN (Subject Alternative Name)
cat > server.conf <<EOF
[req]
distinguished_name = req_distinguished_name
req_extensions = v3_req
prompt = no

[req_distinguished_name]
CN = $SERVICE_NAME.$NAMESPACE.svc

[v3_req]
keyUsage = keyEncipherment, dataEncipherment
extendedKeyUsage = serverAuth
subjectAltName = DNS:$SERVICE_NAME,DNS:$SERVICE_NAME.$NAMESPACE,DNS:${SERVICE_NAME}.$NAMESPACE.svc,DNS:${SERVICE_NAME}.$NAMESPACE.svc.cluster.local
EOF

# Generate certificate signing request
openssl req -new -key tls.key -out tls.csr -config server.conf

# Sign the certificate with CA
openssl x509 -req -days 365 -in tls.csr -CA ca.crt -CAkey ca.key \
  -CAcreateserial -out tls.crt -extensions v3_req -extfile server.conf

# Create/update the TLS secret in the cluster, then add CA cert
echo "Creating/updating secret in cluster..."
CA_CERT=$(base64 < "$CERT_DIR/ca.crt" | tr -d '\r\n')
"$KUBECTL_BIN" create secret tls "$SECRET_NAME" \
  --cert="$CERT_DIR/tls.crt" \
  --key="$CERT_DIR/tls.key" \
  --namespace "$NAMESPACE" \
  --dry-run=client -o yaml | "$KUBECTL_BIN" apply -f -

"$KUBECTL_BIN" patch secret "$SECRET_NAME" \
  --namespace "$NAMESPACE" \
  --type merge \
  -p "{\"data\":{\"ca.crt\":\"$CA_CERT\"}}"

echo "✓ Webhook certificates generated and secret created"
echo "  Namespace: $NAMESPACE"
echo "  Secret: $SECRET_NAME"

# Patch the ValidatingWebhookConfiguration with the CA cert if it exists
echo ""
echo "Patching ValidatingWebhookConfiguration with CA certificate..."
if "$KUBECTL_BIN" get validatingwebhookconfigurations validating-webhook-configuration >/dev/null 2>&1; then
  "$KUBECTL_BIN" patch validatingwebhookconfigurations validating-webhook-configuration --type json -p="[
    {
      \"op\": \"add\",
      \"path\": \"/webhooks/0/clientConfig/caBundle\",
      \"value\": \"$CA_CERT\"
    }
  ]" 2>/dev/null || true
  echo "✓ ValidatingWebhookConfiguration patched with CA certificate"
else
  echo "  (ValidatingWebhookConfiguration not yet deployed, will need to be patched after deployment)"
fi
