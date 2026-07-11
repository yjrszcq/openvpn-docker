#!/usr/bin/env bash
set -euo pipefail

case "${1:-}" in
  x509)
    case " $* " in
      *' -fingerprint '*) printf 'sha256 Fingerprint=FAKE:CA:FINGERPRINT\n' ;;
      *' -pubkey '*) printf '%s\n' '-----BEGIN PUBLIC KEY-----' 'FAKE PUBLIC KEY' '-----END PUBLIC KEY-----' ;;
      *' -purpose '*) printf 'SSL server : Yes\n' ;;
      *' -subject '*) printf 'subject=CN = OpenVPN Container CA\n' ;;
      *) exit 0 ;;
    esac
    ;;
  pkey)
    case " $* " in
      *' -pubout '*) printf '%s\n' '-----BEGIN PUBLIC KEY-----' 'FAKE PUBLIC KEY' '-----END PUBLIC KEY-----' ;;
      *) exit 0 ;;
    esac
    ;;
  verify)
    printf '%s: OK\n' "${@: -1}"
    ;;
  crl)
    case " $* " in
      *' -issuer '*) printf 'issuer=CN = OpenVPN Container CA\n' ;;
      *) exit 0 ;;
    esac
    ;;
  rand)
    printf 'fake-instance-id\n'
    ;;
  *)
    exit 64
    ;;
esac
