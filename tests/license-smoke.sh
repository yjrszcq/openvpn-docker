#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOCKERFILE="$ROOT_DIR/Dockerfile"

for file in LICENSE NOTICE; do
  test -s "$ROOT_DIR/$file"
done

grep -Fq 'GNU GENERAL PUBLIC LICENSE' "$ROOT_DIR/LICENSE"
grep -Fq 'Copyright (C) 2026 yjrszcq' "$ROOT_DIR/NOTICE"
grep -Fq 'SPDX-License-Identifier: GPL-2.0-only' "$ROOT_DIR/NOTICE"
grep -Fq 'COPY --from=builder /work/openvpn/COPYING /usr/local/share/licenses/openvpn/COPYING' "$DOCKERFILE"
grep -Fq 'COPY LICENSE NOTICE /usr/local/share/licenses/openvpn-container/' "$DOCKERFILE"
grep -Fq '## License' "$ROOT_DIR/README.md"
grep -Fq '## 许可证' "$ROOT_DIR/README_CN.md"

printf 'license smoke passed\n'
