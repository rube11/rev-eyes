"""Exercise the production installer's input validation without sudo or deployment."""
import hashlib
import io
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('install-ci-release.sh').read_text()
VALIDATOR = SCRIPT.split("python3 -c '\n", 1)[1].split("\n' \"$stage\"", 1)[0]
BINARY = b'example binary'
CHECKSUM = hashlib.sha256(BINARY).hexdigest().encode() + b'  backend\n'


def bundle(overrides=None, symlink=False):
    files = {'backend': BINARY, 'backend.sha256': CHECKSUM, 'source_commit': b'a' * 40}
    files.update(overrides or {})
    out = io.BytesIO()
    with tarfile.open(fileobj=out, mode='w:gz') as archive:
        for name, data in files.items():
            info = tarfile.TarInfo(name)
            if symlink and name == 'backend':
                info.type = tarfile.SYMTYPE
                info.linkname = '/etc/passwd'
                archive.addfile(info)
            else:
                info.size = len(data)
                archive.addfile(info, io.BytesIO(data))
    return out.getvalue()


class ReleaseInputTests(unittest.TestCase):
    def validate(self, data):
        with tempfile.TemporaryDirectory() as stage:
            return subprocess.run(['python3', '-c', VALIDATOR, stage], input=data,
                                  stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                  timeout=10).returncode

    def test_valid_binary(self):
        self.assertEqual(self.validate(bundle()), 0)

    def test_rejects_bad_inputs(self):
        for data in [bundle({'backend': b'corrupted'}),
                     bundle({'backend.sha256': hashlib.sha256(b'a' * 40).hexdigest().encode()
                             + b'  source_commit\n'}),
                     bundle({'../escape': b'x'}), bundle(symlink=True),
                     bundle({'source_commit': b'not a commit'}),
                     bundle({'backend.sha256': CHECKSUM * 2}),
                     bundle({'source_commit': b'a' * 42}),
                     b'x' * (64 * 1024 * 1024 + 1)]:
            with self.subTest(size=len(data)):
                self.assertNotEqual(self.validate(data), 0)

    def test_stalled_upload_times_out(self):
        with tempfile.TemporaryDirectory() as stage:
            # Shorten only the timer for this otherwise identical validator.
            process = subprocess.Popen(['python3', '-c', VALIDATOR.replace('signal.alarm(60)',
                                       'signal.alarm(1)'), stage], stdin=subprocess.PIPE,
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            try:
                self.assertNotEqual(process.wait(timeout=5), 0)
            finally:
                process.stdin.close()
                process.stdout.close()
                process.stderr.close()


if __name__ == '__main__':
    unittest.main()
