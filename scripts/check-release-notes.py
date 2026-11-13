#!/usr/bin/env python3
"""Exercise GoReleaser's real draft-release path against a loopback GitHub API."""

from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import threading
import time
from urllib.parse import parse_qs, urlparse


def main():
    source = Path(__file__).resolve().parents[1]
    goreleaser = shutil.which(sys.argv[1] if len(sys.argv) > 1 else "goreleaser")
    assert goreleaser, "GoReleaser is required"
    releases, uploads, unexpected = [], [], []
    api = "/api/v3/repos/opper-ai/terraform-provider-opper"

    class GitHub(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def respond(self, code, data):
            body = json.dumps(data).encode()
            self.send_response(code)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_GET(self):
            if self.path == "/api/v3/rate_limit":
                self.respond(200, {"resources": {"core": {
                    "limit": 5000, "remaining": 5000,
                    "reset": int(time.time()) + 3600,
                }}})
            elif self.path == api:
                self.respond(200, {"id": 1, "full_name": "opper-ai/terraform-provider-opper",
                                   "permissions": {"push": True}})
            elif self.path == api + "/releases/tags/v0.1.0":
                self.respond(404, {"message": "Not Found"})
            else:
                unexpected.append(("GET", self.path))
                self.respond(500, {"message": "unexpected request"})

        def do_POST(self):
            body = self.rfile.read(int(self.headers["Content-Length"]))
            if self.path == api + "/releases":
                release = json.loads(body)
                releases.append(release)
                self.respond(201, release | {
                    "id": 1, "html_url": origin + "/draft",
                    "upload_url": origin + "/api/uploads/repos/opper-ai/terraform-provider-opper/releases/1/assets{?name,label}",
                })
            elif urlparse(self.path).path == "/api/uploads/repos/opper-ai/terraform-provider-opper/releases/1/assets":
                name = parse_qs(urlparse(self.path).query)["name"][0]
                uploads.append(name)
                self.respond(201, {"id": len(uploads), "name": name,
                                   "size": len(body), "state": "uploaded"})
            else:
                unexpected.append(("POST", self.path))
                self.respond(500, {"message": "unexpected request"})

    # No real API, signing, or platform credentials reach this subprocess.
    env = {k: os.environ[k] for k in ("PATH", "HOME", "TMPDIR", "SYSTEMROOT") if k in os.environ}
    env.update(GITHUB_TOKEN="local-release-check-token", GOPROXY="off", GOSUMDB="off",
               GOENV="off", GOTOOLCHAIN="local", GIT_CONFIG_NOSYSTEM="1",
               GIT_CONFIG_GLOBAL=os.devnull)
    with tempfile.TemporaryDirectory(prefix="opper-release-notes-") as temporary:
        root = Path(temporary)

        def run(*args):
            result = subprocess.run(args, cwd=root, env=env, capture_output=True,
                                    text=True, timeout=180)
            if result.returncode:
                raise RuntimeError(result.stdout + result.stderr)

        run("git", "init", "--initial-branch=main")
        run("git", "remote", "add", "origin", "https://github.com/opper-ai/terraform-provider-opper.git")
        go_version = next(line for line in (source / "go.mod").read_text().splitlines()
                          if line.startswith("go "))
        (root / "go.mod").write_text(f"module example.test/release-check\n\n{go_version}\n")
        (root / "main.go").write_text('package main\nvar version string\nfunc main() {}\n')
        for name in ("LICENSE", "terraform-registry-manifest.json"):
            shutil.copy2(source / name, root / name)
        notes = "# Local release check\n\nReviewed notes must reach the draft unchanged.\n"
        (root / "notes.md").write_text(notes)
        server = ThreadingHTTPServer(("127.0.0.1", 0), GitHub)
        origin = f"http://127.0.0.1:{server.server_port}"
        config = (source / ".goreleaser.yml").read_text()
        assert "\ngithub_urls:" not in config, "test transport override needs updating"
        (root / ".goreleaser.yml").write_text(config + f"""
github_urls:
  api: {origin}/api/v3/
  upload: {origin}/api/uploads/
  download: {origin}/
""")
        run("git", "add", ".")
        run("git", "-c", "user.name=Release check", "-c", "user.email=release@example.test",
            "commit", "-m", "Release fixture")
        run("git", "tag", "v0.1.0")
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            # Snapshot/publish skips bypass the release-notes path being tested.
            # Keep loopback upload concurrency bounded on high-core machines.
            run(goreleaser, "release", "--clean", "--parallelism=2", "--skip=sign,validate",
                "--release-notes", "notes.md")
        finally:
            server.shutdown()
            thread.join()
            server.server_close()
        assert not unexpected, unexpected
        assert len(releases) == 1, "expected one local draft"
        assert releases[0]["tag_name"] == "v0.1.0"
        assert releases[0]["draft"] is True, "release must remain a draft"
        assert releases[0]["body"].strip() == notes.strip(), "draft release lost the supplied notes"
        expected = {f"terraform-provider-opper_0.1.0_{p}.zip" for p in (
            "darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64",
            "linux_arm", "linux_s390x", "windows_amd64", "windows_arm64",
        )} | {"terraform-provider-opper_0.1.0_SHA256SUMS", "terraform-provider-opper_0.1.0_manifest.json"}
        assert len(uploads) == len(expected) and set(uploads) == expected
    print("Verified supplied release notes, draft status and ten unsigned uploads against loopback GitHub.")


if __name__ == "__main__":
    main()
