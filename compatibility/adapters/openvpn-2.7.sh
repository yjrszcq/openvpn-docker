#!/usr/bin/env bash

OVPN_ADAPTER_NAME=openvpn-2.7
OVPN_ADAPTER_TEMPLATE_FAMILY=openvpn-2.7
OVPN_ADAPTER_CONFIG_TEST_CIPHER=AES-256-GCM

ovpn_adapter_probe_feature() {
  local feature="$1"
  local help_output="$2"

  case "$feature" in
    tls-crypt)
      grep -Fq -- '--tls-crypt key' <<<"$help_output"
      ;;
    data-ciphers)
      grep -Fq -- '--data-ciphers list' <<<"$help_output"
      ;;
    crl-verify)
      grep -Fq -- '--crl-verify crl' <<<"$help_output"
      ;;
    topology-subnet)
      grep -Fq -- '--topology t' <<<"$help_output" && grep -Fq -- "'subnet'" <<<"$help_output"
      ;;
    *)
      return 1
      ;;
  esac
}
