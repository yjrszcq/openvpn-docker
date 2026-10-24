#!/usr/bin/env python3
"""Attach verified archives to an existing Release; never replace an asset."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import tarfile
import urllib.parse
import urllib.request


class PrivateRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        redirected = super().redirect_request(req, fp, code, msg, headers, newurl)
        if redirected is not None and urllib.parse.urlsplit(req.full_url).netloc != urllib.parse.urlsplit(newurl).netloc:
            redirected.remove_header('Authorization')
        return redirected


def verify_archives(directory, tag, commit):
    root = Path(__file__).resolve().parent.parent
    versions = dict(line.split('=', 1) for line in (root / 'versions.env').read_text().splitlines() if '=' in line and not line.startswith('#'))
    go_version = re.search(r'^go ([0-9]+\.[0-9]+\.[0-9]+)$', (root / 'go.mod').read_text(), re.MULTILINE).group(1)
    if not re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+', tag) or tag != 'v' + versions['IMAGE_VERSION']:
        raise ValueError('Release tag must match the project version')
    if not re.fullmatch(r'[0-9a-f]{40}', commit):
        raise ValueError('expected a full source commit SHA')
    names = [f'openvpn-server-{tag[1:]}-linux-{arch}.tar.gz' for arch in ('amd64', 'arm64')]
    checksums = {}
    for line in (directory / 'SHA256SUMS').read_text().splitlines():
        digest, name = line.split()
        if name in checksums or not re.fullmatch(r'[0-9a-f]{64}', digest):
            raise ValueError('invalid checksum inventory')
        checksums[name] = digest
    if set(checksums) != set(names):
        raise ValueError('both architectures are required in SHA256SUMS')
    for arch, name in zip(('amd64', 'arm64'), names):
        path = directory / name
        if hashlib.sha256(path.read_bytes()).hexdigest() != checksums[name]:
            raise ValueError(f'archive checksum mismatch: {name}')
        with tarfile.open(path) as stream:
            metadata = json.load(stream.extractfile('openvpn-server/runtime/manifest.json'))
        if any(metadata.get(key) != value for key, value in {
            'version': tag[1:], 'architecture': arch, 'commit': commit,
            'data_schema': int(versions['DATA_SCHEMA']), 'openvpn_version': versions['OPENVPN_VERSION'],
            'go_version': go_version,
        }.items()):
            raise ValueError(f'archive provenance mismatch: {name}')
    names.append('SHA256SUMS')
    return names


def publish(directory, tag, commit, repository, api, token):
    names = verify_archives(directory, tag, commit)
    opener = urllib.request.build_opener(PrivateRedirect())

    def request(url, data=None, binary=False):
        headers = {'Authorization': f'Bearer {token}', 'Accept': 'application/octet-stream' if binary else 'application/vnd.github+json',
                   'X-GitHub-Api-Version': '2022-11-28', 'User-Agent': 'openvpn-bundle-release'}
        if data is not None:
            headers['Content-Type'] = 'application/octet-stream'
        with opener.open(urllib.request.Request(url, data=data, headers=headers), timeout=120) as response:
            body = response.read()
        return body if binary else json.loads(body)

    endpoint = api.rstrip('/') + '/repos/' + repository
    release = request(endpoint + '/releases/tags/' + tag)
    if release['tag_name'] != tag or release.get('draft'):
        raise ValueError('an existing published matching Release is required')
    if request(endpoint + '/commits/' + tag)['sha'] != commit:
        raise ValueError('Release tag moved after the source build')
    existing = {}
    page = 1
    while True:
        assets = request(endpoint + f'/releases/{release["id"]}/assets?per_page=100&page={page}')
        for asset in assets:
            if asset['name'] in existing:
                raise ValueError('duplicate existing asset name')
            existing[asset['name']] = asset
        if len(assets) < 100:
            break
        page += 1
    # Preflight every existing attachment before the first write. Never clobber.
    pending = []
    for name in names:
        data = (directory / name).read_bytes()
        asset = existing.get(name)
        if asset:
            remote = request(endpoint + f'/releases/assets/{asset["id"]}', binary=True)
            if hashlib.sha256(remote).digest() != hashlib.sha256(data).digest():
                raise ValueError(f'existing asset differs: {name}')
            print(f'unchanged attachment: {name}')
        else:
            pending.append(name)
    upload = release['upload_url'].split('{', 1)[0]
    for name in pending:
        data = (directory / name).read_bytes()
        result = request(upload + '?' + urllib.parse.urlencode({'name': name}), data=data)
        if result.get('name') != name or result.get('size') != len(data) or result.get('state') != 'uploaded':
            raise ValueError(f'upload verification failed: {name}')
        print(f'attached: {name}')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', type=Path, required=True)
    parser.add_argument('--tag', required=True)
    parser.add_argument('--commit', required=True)
    parser.add_argument('--repository', default=os.environ.get('GITHUB_REPOSITORY', 'yjrszcq/openvpn-docker'))
    parser.add_argument('--api-url', default='https://api.github.com')
    args = parser.parse_args()
    publish(args.directory, args.tag, args.commit, args.repository, args.api_url, os.environ['GH_TOKEN'])
