"""Exercise real HTTP upload/preflight logic without contacting GitHub."""
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import io
import json
from pathlib import Path
import sys
import tarfile
import tempfile
import threading
import unittest
import urllib.parse

ROOT = Path(__file__).resolve().parents[3]
sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('release_assets', ROOT / 'scripts/release-bundle-assets.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
VERSIONS = dict(line.split('=', 1) for line in (ROOT / 'versions.env').read_text().splitlines() if '=' in line and not line.startswith('#'))


class ReleaseAssets(unittest.TestCase):
    def setUp(self):
        self.work = tempfile.TemporaryDirectory()
        self.addCleanup(self.work.cleanup)
        self.directory = Path(self.work.name)
        self.tag = 'v' + VERSIONS['IMAGE_VERSION']
        self.commit = '1' * 40
        self.existing = {}
        self.uploads = []
        self.present = True
        self.draft = False
        self.remote_commit = self.commit
        self.names = []
        sums = []
        for arch in ('amd64', 'arm64'):
            name = f'openvpn-server-{self.tag[1:]}-linux-{arch}.tar.gz'
            self.names.append(name)
            data = json.dumps({'version': self.tag[1:], 'architecture': arch, 'commit': self.commit,
                               'data_schema': int(VERSIONS['DATA_SCHEMA']), 'openvpn_version': VERSIONS['OPENVPN_VERSION'],
                               'go_version': VERSIONS['GO_RUNTIME_VERSION']}).encode()
            with tarfile.open(self.directory / name, 'w:gz') as stream:
                member = tarfile.TarInfo('openvpn-server/runtime/manifest.json')
                member.size = len(data)
                stream.addfile(member, io.BytesIO(data))
            sums.append(f'{hashlib.sha256((self.directory / name).read_bytes()).hexdigest()}  {name}\n')
        (self.directory / 'SHA256SUMS').write_text(''.join(sums))
        self.names.append('SHA256SUMS')
        state = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def respond(self, data, status=200):
                self.send_response(status)
                self.end_headers()
                self.wfile.write(data if isinstance(data, bytes) else json.dumps(data).encode())

            def do_GET(self):
                path = urllib.parse.urlsplit(self.path).path
                if '/releases/tags/' in path:
                    if not state.present:
                        self.respond({}, 404)
                    else:
                        self.respond({'id': 7, 'tag_name': state.tag, 'draft': state.draft, 'upload_url': state.api + '/upload{?name,label}'})
                elif '/commits/' in path:
                    self.respond({'sha': state.remote_commit})
                elif path.endswith('/releases/7/assets'):
                    page = int(urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query)['page'][0])
                    assets = [{'id': index, 'name': name} for index, name in enumerate(state.existing)]
                    self.respond(assets[(page - 1) * 100:page * 100])
                elif '/releases/assets/' in path:
                    self.respond(list(state.existing.values())[int(path.rsplit('/', 1)[1])])
                else:
                    self.respond({}, 404)

            def do_POST(self):
                name = urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query)['name'][0]
                data = self.rfile.read(int(self.headers['Content-Length']))
                state.uploads.append(name)
                state.existing[name] = data
                self.respond({'name': name, 'size': len(data), 'state': 'uploaded'})

        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.api = f'http://127.0.0.1:{self.server.server_port}'
        thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(self.server.server_close)
        self.addCleanup(self.server.shutdown)

    def publish(self):
        module.publish(self.directory, self.tag, self.commit, 'owner/project', self.api, 'test-token')

    def test_upload_complete_set_and_identical_retry(self):
        self.publish()
        self.assertEqual(self.uploads, self.names)
        self.publish()
        self.assertEqual(self.uploads, self.names)

    def test_conflict_preflight_writes_nothing(self):
        self.existing['SHA256SUMS'] = b'conflict'
        with self.assertRaisesRegex(ValueError, 'existing asset differs'):
            self.publish()
        self.assertEqual(self.uploads, [])

    def test_paginated_identical_attachments(self):
        self.existing.update({f'other-{index}': b'other' for index in range(100)})
        for name in self.names:
            self.existing[name] = (self.directory / name).read_bytes()
        self.publish()
        self.assertEqual(self.uploads, [])

    def test_cross_origin_redirect_removes_token(self):
        request = module.urllib.request.Request(self.api + '/asset', headers={'Authorization': 'Bearer secret'})
        redirected = module.PrivateRedirect().redirect_request(request, None, 302, 'Found', {}, 'https://objects.example/asset')
        self.assertIsNone(redirected.get_header('Authorization'))
        redirected = module.PrivateRedirect().redirect_request(request, None, 302, 'Found', {}, self.api + '/same-origin')
        self.assertEqual(redirected.get_header('Authorization'), 'Bearer secret')

    def test_missing_release_never_created(self):
        self.present = False
        with self.assertRaises(Exception):
            self.publish()
        self.assertEqual(self.uploads, [])

    def test_draft_and_moved_tag_refused(self):
        self.draft = True
        with self.assertRaisesRegex(ValueError, 'published matching Release'):
            self.publish()
        self.draft = False
        self.remote_commit = '2' * 40
        with self.assertRaisesRegex(ValueError, 'tag moved'):
            self.publish()
        self.assertEqual(self.uploads, [])

    def test_corrupt_or_incomplete_assets_refused(self):
        path = self.directory / self.names[0]
        path.write_bytes(b'corrupt')
        with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
            self.publish()
        self.assertEqual(self.uploads, [])
        (self.directory / 'SHA256SUMS').write_text('')
        with self.assertRaisesRegex(ValueError, 'both architectures'):
            self.publish()


if __name__ == '__main__':
    unittest.main()
