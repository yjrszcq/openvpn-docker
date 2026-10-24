"""Assemble a private musl runtime; no libraries are installed on the host."""
import hashlib
import json
import os
import platform
import shutil
import subprocess
from elftools.elf.elffile import ELFFile
from pathlib import Path

root = Path('/bundle')
lib = root / 'runtime/lib'
lib.mkdir(parents=True)
raw = root / 'runtime/bin'
for name, source in {'openssl': '/usr/bin/openssl', 'ip': shutil.which('ip'),
                     'xtables-nft-multi': '/usr/sbin/xtables-nft-multi',
                     'bash': '/bin/bash', 'nano': '/usr/bin/nano',
                     'flock': shutil.which('flock')}.items():
    shutil.copy2(source, raw / name)
shutil.copy2('/bin/busybox.static', raw / 'busybox')
for name in ('iptables', 'ip6tables', 'iptables-save', 'iptables-restore'):
    (raw / name).symlink_to('xtables-nft-multi')
for source, target in [('/usr/share/easy-rsa', 'runtime/share/easy-rsa'),
                       ('/usr/lib/xtables', 'runtime/lib/xtables')]:
    shutil.copytree(source, root / target, symlinks=False, dirs_exist_ok=True)
ssl = root / 'runtime/etc/ssl'
ssl.mkdir(parents=True)
for name in ('openssl.cnf', 'cert.pem'):
    shutil.copy2(Path('/etc/ssl') / name, ssl / name)
shutil.copytree('/etc/terminfo', root / 'runtime/share/terminfo', dirs_exist_ok=True)
providers = Path('/usr/lib/ossl-modules')
if providers.exists():
    shutil.copytree(providers, lib / 'ossl-modules', symlinks=False)
# Walk ELF DT_NEEDED recursively, including dlopen modules. Fail closed when
# any dependency is missing; the private loader uses this flat library set.
queue = list(raw.iterdir()) + list(lib.rglob('*.so'))
seen = set()
while queue:
    path = queue.pop()
    if path.is_symlink() or path in seen or path.read_bytes()[:4] != b'\x7fELF':
        continue
    seen.add(path)
    with path.open('rb') as stream:
        dynamic = ELFFile(stream).get_section_by_name('.dynamic')
        needed = [] if dynamic is None else [tag.needed for tag in dynamic.iter_tags() if tag.entry.d_tag == 'DT_NEEDED']
    for name in needed:
        dependency = next((directory / name for directory in (Path('/lib'), Path('/usr/lib')) if (directory / name).is_file()), None)
        if dependency is None:
            raise RuntimeError(f'unresolved dependency: {path}: {name}')
        target = lib / name
        if not target.exists():
            shutil.copy2(dependency, target)
            queue.append(target)
loader = next(Path('/lib').glob('ld-musl-*.so.1'))
shutil.copy2(loader, lib / 'ld-musl.so.1')
# Keep shell command wrappers private: do not shadow commands outside the bundle.
bindir = root / 'bin'
bindir.mkdir()
names = set(subprocess.check_output(['/bin/busybox.static', '--list'], text=True).splitlines())
names.update(('ovpn', 'ovpn-hook', 'ovpn-broker', 'ovpn-api', 'openvpn', 'openssl',
              'ip', 'iptables', 'ip6tables', 'iptables-save', 'iptables-restore',
              'easyrsa', 'bash', 'nano', 'flock'))
for name in sorted(names):
    (bindir / name).symlink_to('../ovpn')
for name in ('data', 'run'):
    (root / name).mkdir(mode=0o700)
(root / 'logs').symlink_to('data/logs')
# Preserve package license texts and package inventory for redistribution.
licenses = root / 'runtime/licenses'
shutil.copy2('/usr/local/go/LICENSE', licenses / 'Go-LICENSE')
shutil.copy2('/tmp/openvpn/COPYING', licenses / 'OpenVPN-COPYING')
shutil.copytree('/usr/share/spdx/text', licenses / 'spdx', dirs_exist_ok=True)
module_cache = Path(subprocess.check_output(['go', 'env', 'GOMODCACHE'], text=True).strip())
for module in ('github.com/mattn/go-sqlite3@v1.14.48', 'go.yaml.in/yaml/v3@v3.0.4'):
    directory = licenses / 'go-modules' / module
    directory.mkdir(parents=True)
    for source in (module_cache / module).iterdir():
        if source.is_file() and source.name.upper().startswith(('LICENSE', 'NOTICE', 'COPYING')):
            shutil.copy2(source, directory / source.name)
(licenses / 'SOURCE-OFFERS.txt').write_text('Alpine package source recipes and source URLs: https://gitlab.alpinelinux.org/alpine/aports/-/tree/3.24-stable\nOpenVPN source: https://swupdate.openvpn.org/community/releases/openvpn-' + os.environ['OPENVPN_VERSION'] + '.tar.gz\nGo source: https://go.dev/dl/\nPackage versions and upstream license metadata are recorded alongside this file.\n')
(licenses / 'package-license-metadata.txt').write_text(subprocess.check_output(['apk', 'info', '--license'], text=True))
(licenses / 'packages.txt').write_text(subprocess.check_output(['apk', 'info', '-v'], text=True))
arch = {'x86_64': 'amd64', 'aarch64': 'arm64'}[platform.machine()]
manifest = {'format': 1, 'version': os.environ['IMAGE_VERSION'], 'data_schema': 4,
            'architecture': arch, 'go_version': '1.27.3',
            'openvpn_version': os.environ['OPENVPN_VERSION'],
            'commit': os.environ['VCS_REF'], 'build_date': os.environ['BUILD_DATE']}
(root / 'runtime/manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
# Hash regular program/resource files. Symlink targets are checked separately by
# the installer; mutable user directories are excluded from the manifest.
lines = []
for path in sorted(root.rglob('*')):
    if path.is_file() and not path.is_symlink():
        lines.append(f'{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.relative_to(root)}\n')
(root / 'MANIFEST.sha256').write_text(''.join(lines))
