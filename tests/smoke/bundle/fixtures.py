"""Controlled upgrade candidates made from actual packaged binaries.

Version-string replacements are fixture-only; shipping packages use source builds.
"""
import hashlib
import json
import io
import shutil
import sys
import tarfile
from pathlib import Path

archive, out = map(Path, sys.argv[1:])
releases = out / 'releases'
releases.mkdir(exist_ok=True, parents=True)
with tarfile.open(archive) as stream:
    stream.extractall(releases, filter='data')
source = releases / 'openvpn-server'
metadata = json.loads((source / 'runtime/manifest.json').read_text())
new = metadata['version']
major, minor, patch = map(int, new.split('.'))
assert minor > 0 and patch < 9
old = f'{major}.{minor-1}.{patch}'
bad = f'{major}.{minor}.{patch+1}'
middle = f'{major}.{minor-1}.{patch+1}'
for name, version in [('old', old), ('middle', middle), ('new', new), ('bad', bad)]:
    (out / f'{name}-version').write_text(version)
    stage = out / f'candidate-{name}'
    shutil.copytree(source, stage, symlinks=True)
    (stage / 'runtime/manifest.json').write_text(json.dumps(dict(metadata, version=version), indent=2) + '\n')
    for binary in ('ovpn-core', 'ovpn-broker', 'ovpn-api'):
        path = stage / 'runtime/bin' / binary
        assert len(version) == len(new)
        data = path.read_bytes()
        assert new.encode() in data
        path.write_bytes(data.replace(new.encode(), version.encode()))
    if name == 'bad':
        shutil.copy2(stage / 'runtime/bin/busybox', stage / 'runtime/bin/openvpn')
    lines = []
    for path in sorted(stage.rglob('*')):
        if path.is_file() and not path.is_symlink() and path.name != 'MANIFEST.sha256':
            lines.append(f'{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.relative_to(stage)}\n')
    (stage / 'MANIFEST.sha256').write_text(''.join(lines))
    directory = releases / 'bad' if name == 'bad' else releases
    directory.mkdir(exist_ok=True)
    target = directory / f"openvpn-server-{version}-linux-{metadata['architecture']}.tar.gz"
    with tarfile.open(target, 'w:gz') as stream:
        stream.add(stage, arcname='openvpn-server')
    with (directory / 'SHA256SUMS').open('a') as stream:
        stream.write(f'{hashlib.sha256(target.read_bytes()).hexdigest()}  {target.name}\n')
    shutil.rmtree(stage)
# Minimal hostile assets exercise validation before any payload is executed.
for name in ('unsafe', 'wrong-arch', 'wrong-schema', 'unlisted'):
    directory = releases / name
    directory.mkdir()
    target = directory / f"openvpn-server-{new}-linux-{metadata['architecture']}.tar.gz"
    files = {'ovpn': b'not executable\n'}
    manifest = dict(metadata)
    if name == 'wrong-arch':
        manifest['architecture'] = 'arm64' if metadata['architecture'] == 'amd64' else 'amd64'
    if name == 'wrong-schema':
        manifest['data_schema'] = 999
    files['runtime/manifest.json'] = (json.dumps(manifest, indent=2) + '\n').encode()
    files['MANIFEST.sha256'] = ''.join(f'{hashlib.sha256(data).hexdigest()}  {path}\n' for path, data in files.items()).encode()
    if name == 'unlisted':
        files['runtime/unlisted'] = b'not in inventory\n'
    if name == 'unsafe':
        files['../escaped'] = b'unsafe\n'
    with tarfile.open(target, 'w:gz') as stream:
        for path, data in files.items():
            info = tarfile.TarInfo('openvpn-server/' + path)
            info.size = len(data)
            stream.addfile(info, io.BytesIO(data))
    (directory / 'SHA256SUMS').write_text(f'{hashlib.sha256(target.read_bytes()).hexdigest()}  {target.name}\n')
directory = releases / 'corrupt'
directory.mkdir()
target = directory / f"openvpn-server-{new}-linux-{metadata['architecture']}.tar.gz"
target.write_bytes(b'corrupt download\n')
(directory / 'SHA256SUMS').write_text(f'{"0" * 64}  {target.name}\n')
(releases / 'missing').mkdir()
