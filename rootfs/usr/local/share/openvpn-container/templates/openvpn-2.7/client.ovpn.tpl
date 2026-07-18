client
# ovpn-client-id: {{CLIENT_ID}}
# ovpn-client-name: {{CLIENT_NAME}}
dev tun
proto {{OVPN_CLIENT_PROTO}}
remote {{OVPN_ENDPOINT}} {{OVPN_PORT}}

resolv-retry infinite
nobind

persist-key
persist-tun

remote-cert-tls server

data-ciphers AES-256-GCM:CHACHA20-POLY1305:AES-128-GCM

<ca>
{{CA_CERT}}
</ca>

<cert>
{{CLIENT_CERT}}
</cert>

<key>
{{CLIENT_KEY}}
</key>

<tls-crypt>
{{TLS_CRYPT_KEY}}
</tls-crypt>
