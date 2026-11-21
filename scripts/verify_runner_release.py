"""Verify and build the exact public source referenced by local Agent instructions."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
manifest = json.loads((ROOT / 'apps/web/src/runner-release.ts').read_text().split(' = ', 1)[1].split(' as const', 1)[0])
assert manifest['url'] == 'https://codeload.github.com/Leon-KTlan/inferlab/tar.gz/' + manifest['commit']
with urllib.request.urlopen(manifest['url'], timeout=60) as response:
    data = response.read(10 * 1024 * 1024 + 1)
assert len(data) <= 10 * 1024 * 1024
assert hashlib.sha256(data).hexdigest() == manifest['sha256'], 'Source SHA-256 mismatch'
with tempfile.TemporaryDirectory(prefix='inferlab-pinned-source-') as directory:
    destination = Path(directory)
    archive = destination / 'source.tar.gz'
    archive.write_bytes(data)
    source = destination / 'source'
    source.mkdir()
    with tarfile.open(archive) as bundle:
        for member in bundle.getmembers():
            assert member.isfile() or member.isdir(), 'Archive links/devices are not permitted'
            resolved = (source / member.name).resolve()
            assert source.resolve() in resolved.parents, 'Archive path traversal'
        bundle.extractall(source)
    repository = next(source.iterdir())
    subprocess.run(['go', 'build', '-trimpath', '-o', str(destination / 'inferlab'), '.'], cwd=repository / 'services/api', env={**os.environ, 'GOTOOLCHAIN': 'local'}, check=True)
output = ROOT / 'docs/tasks/dual-execution/evidence/pinned-source.json'
output.parent.mkdir(parents=True, exist_ok=True)
output.write_text(json.dumps({'result': 'passed', **manifest, 'verified_archive_bytes': len(data), 'build': 'go build -trimpath; GOTOOLCHAIN=local'}, indent=2) + '\n')
print('Pinned public source hash and Go tool build passed.')
