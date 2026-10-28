#!/usr/bin/env bash
# Run inside GitHub Actions, in a job outside the prod environment. Asks GitHub for the job's OIDC token and offers it
# to Entra as the deploy identity, the way azure/login does, and passes only when Entra refuses it for having no
# matching federated credential.

set -euo pipefail

if [[ -z ${ACTIONS_ID_TOKEN_REQUEST_URL:-} ]]; then
  echo "ok   this run gets no OIDC token at all, so it cannot sign in to Azure"
  exit 0
fi

assertion=$(curl -fsS -H "Authorization: Bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
  "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=api://AzureADTokenExchange" | jq -r .value)
payload=$(cut -d. -f2 <<<"$assertion" | tr '_-' '/+')
while ((${#payload} % 4)); do payload+="="; done
subject=$(base64 -d <<<"$payload" | jq -r .sub)

response=$(curl -sS "https://login.microsoftonline.com/$AZURE_TENANT_ID/oauth2/v2.0/token" \
  --data-urlencode "client_id=$AZURE_CLIENT_ID" \
  --data-urlencode "scope=https://management.azure.com/.default" \
  --data-urlencode "grant_type=client_credentials" \
  --data-urlencode "client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer" \
  --data-urlencode "client_assertion=$assertion")

if jq -e 'has("access_token")' <<<"$response" >/dev/null; then
  echo "FAIL a run with subject $subject signed in to Azure as the deploy identity"
  exit 1
fi
if jq -r .error_description <<<"$response" | grep -q AADSTS700213; then
  echo "ok   Entra refuses subject $subject: no federated credential matches it"
  exit 0
fi
echo "FAIL Entra refused the sign-in for another reason: $(jq -r .error_description <<<"$response" | head -1)"
exit 1
