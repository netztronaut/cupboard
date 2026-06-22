#!/usr/bin/env bash
set -euo pipefail

NAMESPACE=${1:-authentik}
APP_SLUG=${2:-cupboard}
CLIENT_ID=${3:-cupboard-local}
REDIRECT_URI=${4:-http://cupboard.localhost/auth/callback}
LAUNCH_URL=${5:-http://cupboard.localhost/}
DEMO_USER=${6:-cupboard-admin}
DEMO_PASSWORD=${7:-cupboard-admin}
KUBECTL_CMD=("${KUBECTL:-kubectl}")
if [[ -n "${KUBECTL_CONTEXT:-}" ]]; then
  KUBECTL_CMD+=(--context "$KUBECTL_CONTEXT")
fi

if ! "${KUBECTL_CMD[@]}" get deployment authentik-server -n "$NAMESPACE" >/dev/null 2>&1; then
  echo "Authentik deployment not found in namespace '$NAMESPACE'; skipping OIDC bootstrap"
  exit 0
fi

echo "Waiting for Authentik bootstrap to complete (default flows must exist)..."
BOOTSTRAP_TIMEOUT=300
BOOTSTRAP_ELAPSED=0
until "${KUBECTL_CMD[@]}" exec -n "$NAMESPACE" deployment/authentik-server -- \
    ak shell -c "from authentik.flows.models import Flow; Flow.objects.get(slug='default-provider-authorization-implicit-consent')" \
    >/dev/null 2>&1; do
  if [[ $BOOTSTRAP_ELAPSED -ge $BOOTSTRAP_TIMEOUT ]]; then
    echo "Timed out waiting for Authentik bootstrap after ${BOOTSTRAP_TIMEOUT}s"
    exit 1
  fi
  echo "  Authentik bootstrap not ready yet, retrying in 10s..."
  sleep 10
  BOOTSTRAP_ELAPSED=$((BOOTSTRAP_ELAPSED + 10))
done

echo "Configuring Authentik OIDC application for local testing..."
"${KUBECTL_CMD[@]}" exec -n "$NAMESPACE" deployment/authentik-server -i -- ak shell <<PY
from authentik.core.models import Application, User
from authentik.flows.models import Flow
from authentik.providers.oauth2.models import OAuth2Provider, RedirectURI, RedirectURIMatchingMode, ScopeMapping

app_slug = "${APP_SLUG}"
client_id = "${CLIENT_ID}"
redirect_uri = "${REDIRECT_URI}"
launch_url = "${LAUNCH_URL}"
demo_user = "${DEMO_USER}"
demo_password = "${DEMO_PASSWORD}"

authorization_flow = Flow.objects.get(slug="default-provider-authorization-implicit-consent")
invalidation_flow = Flow.objects.get(slug="default-provider-invalidation-flow")

provider, _ = OAuth2Provider.objects.update_or_create(
    name="Cupboard Local",
    defaults={
        "authorization_flow": authorization_flow,
        "invalidation_flow": invalidation_flow,
        "client_type": "public",
        "client_id": client_id,
        "client_secret": "",
        "grant_types": ["authorization_code", "refresh_token"],
        "_redirect_uris": [RedirectURI(RedirectURIMatchingMode.STRICT, redirect_uri).__dict__],
        "include_claims_in_id_token": True,
        "sub_mode": "user_email",
        "issuer_mode": "per_provider",
    },
)
provider.property_mappings.set(
    ScopeMapping.objects.filter(scope_name__in=["openid", "profile", "email"])
)
Application.objects.update_or_create(
    slug=app_slug,
    defaults={
        "name": "Cupboard Local",
        "provider": provider,
        "meta_launch_url": launch_url,
    },
)

user, _ = User.objects.get_or_create(
    username=demo_user,
    defaults={
        "email": f"{demo_user}@localhost",
        "name": "Cupboard Admin",
        "is_active": True,
    },
)
user.email = f"{demo_user}@localhost"
user.name = "Cupboard Admin"
user.is_active = True
user.save()
user.set_password(demo_password)
user.save()

print({
    "application": app_slug,
    "client_id": client_id,
    "redirect_uri": redirect_uri,
    "demo_user": demo_user,
})
PY
