#!/usr/bin/env bash
set -euo pipefail

if [ -n "${FAKE_OPENSSL_LOG:-}" ]; then
  printf '%s\n' "${1:-}" >>"$FAKE_OPENSSL_LOG"
fi
if [ "${FAKE_OPENSSL_SLEEP_ON:-}" = "${1:-}" ]; then
  sleep "${FAKE_OPENSSL_SLEEP_SECONDS:-1}"
fi
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
      *' -nextupdate '*) printf 'nextUpdate=Jan  1 00:00:00 3000 GMT\n' ;;
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
