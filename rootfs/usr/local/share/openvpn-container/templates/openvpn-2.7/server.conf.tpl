port {{OVPN_PORT}}
proto {{OVPN_PROTO}}
dev tun

topology subnet
server {{OVPN_NETWORK_ADDRESS}} {{OVPN_NETWORK_NETMASK}} nopool
{{OVPN_DYNAMIC_POOL_DIRECTIVE}}
client-config-dir {{OVPN_CCD_DIR}}
ifconfig-pool-persist {{OVPN_POOL_PERSIST_FILE}}
management {{OVPN_MANAGEMENT_SOCKET}} unix


ca {{OVPN_DATA_DIR}}/pki/ca.crt
cert {{OVPN_DATA_DIR}}/pki/issued/openvpn-server.crt
key {{OVPN_DATA_DIR}}/pki/private/openvpn-server.key

dh none

tls-crypt {{OVPN_DATA_DIR}}/secrets/tls-crypt.key
crl-verify {{OVPN_DATA_DIR}}/pki/crl.pem

keepalive 10 120
persist-key
persist-tun

data-ciphers AES-256-GCM:CHACHA20-POLY1305:AES-128-GCM

{{OVPN_CLIENT_TO_CLIENT_DIRECTIVE}}
{{OVPN_REDIRECT_GATEWAY_PUSH}}
{{OVPN_ROUTE_PUSHES}}
{{OVPN_DNS_PUSHES}}
verb 3
