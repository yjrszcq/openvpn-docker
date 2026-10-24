port {{OVPN_PORT}}
proto {{OVPN_SERVER_PROTO}}
{{OVPN_BIND_DIRECTIVE}}
dev tun

topology subnet
server {{OVPN_NETWORK_ADDRESS}} {{OVPN_NETWORK_NETMASK}} nopool
{{OVPN_DYNAMIC_POOL_DIRECTIVE}}
client-config-dir {{OVPN_CCD_DIR}}
script-security 2
{{OVPN_HOOK_ENV}}client-connect {{OVPN_HOOK_COMMAND}}
client-disconnect {{OVPN_HOOK_COMMAND}}
management {{OVPN_OPENVPN_MANAGEMENT_SOCKET}} unix
management-client-user root


ca {{FILE_pki_ca_crt}}
cert {{FILE_pki_issued_openvpn_server_crt}}
key {{FILE_pki_private_openvpn_server_key}}

dh none

tls-crypt {{FILE_secrets_tls_crypt_key}}
crl-verify {{FILE_pki_crl_pem}}

keepalive 10 120
persist-key
persist-tun

data-ciphers AES-256-GCM:CHACHA20-POLY1305:AES-128-GCM

{{OVPN_CLIENT_TO_CLIENT_DIRECTIVE}}
{{OVPN_REDIRECT_GATEWAY_PUSH}}
{{OVPN_ROUTE_PUSHES}}
{{OVPN_DNS_PUSHES}}
verb 3
