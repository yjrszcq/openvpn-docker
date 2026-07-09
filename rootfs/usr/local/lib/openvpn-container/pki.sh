#!/usr/bin/env bash

ovpn_easyrsa_bin() {
  if [ -n "${OVPN_EASYRSA_BIN:-}" ]; then
    printf '%s\n' "$OVPN_EASYRSA_BIN"
    return 0
  fi
  if command -v easyrsa >/dev/null 2>&1; then
    command -v easyrsa
    return 0
  fi
  if [ -x /usr/share/easy-rsa/easyrsa ]; then
    printf '%s\n' /usr/share/easy-rsa/easyrsa
    return 0
  fi
  return 1
}

ovpn_openvpn_bin() {
  if [ -n "${OVPN_OPENVPN_BIN:-}" ]; then
    printf '%s\n' "$OVPN_OPENVPN_BIN"
    return 0
  fi
  command -v openvpn
}

ovpn_run_easyrsa() {
  local bin
  bin="$(ovpn_easyrsa_bin)" || ovpn_die "easyrsa is required for initialization"
  EASYRSA_BATCH=1 EASYRSA_PKI="$OVPN_DATA_DIR/pki" "$bin" "$@"
}

ovpn_pki_init() {
  ovpn_run_easyrsa init-pki
  EASYRSA_REQ_CN="OpenVPN Container CA" ovpn_run_easyrsa build-ca nopass
  EASYRSA_REQ_CN="$OVPN_SERVER_NAME" ovpn_run_easyrsa build-server-full "$OVPN_SERVER_NAME" nopass
  ovpn_run_easyrsa gen-crl

  [ -r "$OVPN_DATA_DIR/pki/ca.crt" ] || ovpn_die "Easy-RSA did not create ca.crt"
  [ -r "$OVPN_DATA_DIR/pki/private/ca.key" ] || ovpn_die "Easy-RSA did not create ca.key"
  [ -r "$OVPN_DATA_DIR/pki/issued/$OVPN_SERVER_NAME.crt" ] || ovpn_die "Easy-RSA did not create server cert"
  [ -r "$OVPN_DATA_DIR/pki/private/$OVPN_SERVER_NAME.key" ] || ovpn_die "Easy-RSA did not create server key"
  [ -r "$OVPN_DATA_DIR/pki/crl.pem" ] || ovpn_die "Easy-RSA did not create CRL"

  chmod 600 "$OVPN_DATA_DIR/pki/private/ca.key" "$OVPN_DATA_DIR/pki/private/$OVPN_SERVER_NAME.key"
  chmod 644 "$OVPN_DATA_DIR/pki/ca.crt" "$OVPN_DATA_DIR/pki/issued/$OVPN_SERVER_NAME.crt" "$OVPN_DATA_DIR/pki/crl.pem"
}

ovpn_tls_crypt_generate() {
  local bin
  bin="$(ovpn_openvpn_bin)" || ovpn_die "openvpn is required to generate tls-crypt key"
  mkdir -p "$OVPN_DATA_DIR/secrets"
  "$bin" --genkey secret "$OVPN_DATA_DIR/secrets/tls-crypt.key"
  chmod 600 "$OVPN_DATA_DIR/secrets/tls-crypt.key"
}


ovpn_pki_issue_client() {
  local name="$1"
  EASYRSA_REQ_CN="$name" ovpn_run_easyrsa build-client-full "$name" nopass
  [ -r "$OVPN_DATA_DIR/pki/issued/$name.crt" ] || ovpn_die "Easy-RSA did not create client cert for $name"
  [ -r "$OVPN_DATA_DIR/pki/private/$name.key" ] || ovpn_die "Easy-RSA did not create client key for $name"
  chmod 644 "$OVPN_DATA_DIR/pki/issued/$name.crt"
  chmod 600 "$OVPN_DATA_DIR/pki/private/$name.key"
}

ovpn_pki_revoke_client() {
  local name="$1"
  ovpn_run_easyrsa revoke "$name"
  ovpn_run_easyrsa gen-crl
  [ -s "$OVPN_DATA_DIR/pki/crl.pem" ] || ovpn_die "Easy-RSA did not refresh CRL"
  chmod 644 "$OVPN_DATA_DIR/pki/crl.pem"
}
