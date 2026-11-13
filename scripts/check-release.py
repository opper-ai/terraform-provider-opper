#!/usr/bin/env python3
"""Check snapshot assets and install the host package without Registry/API access."""

import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import zipfile

TERRAFORM = os.environ.get("TERRAFORM", "terraform")


def terraform(*args, cwd, env):
    return subprocess.check_output(
        [TERRAFORM, *args], cwd=cwd, env=env, text=True
    )


def main():
    dist = Path(sys.argv[1]).resolve()
    prefix = "terraform-provider-opper_"
    sums = list(dist.glob(f"{prefix}*_SHA256SUMS"))
    assert len(sums) == 1, "expected one checksum file"
    version = sums[0].name.removeprefix(prefix).removesuffix("_SHA256SUMS")
    platforms = {
        "darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64",
        "linux_arm", "linux_s390x", "windows_amd64", "windows_arm64",
    }
    expected = {f"{prefix}{version}_{p}.zip" for p in platforms}
    manifest_name = f"{prefix}{version}_manifest.json"
    expected.add(manifest_name)
    # GoReleaser renames this source file on upload, not in snapshot dist/.
    manifest_path = Path(__file__).resolve().parents[1] / "terraform-registry-manifest.json"
    entries = {}
    for line in sums[0].read_text().splitlines():
        digest, name = line.split()
        assert name not in entries, f"duplicate checksum: {name}"
        entries[name] = digest
    assert set(entries) == expected, "unexpected or missing release assets"
    for name, digest in entries.items():
        path = manifest_path if name == manifest_name else dist / name
        assert hashlib.sha256(path.read_bytes()).hexdigest() == digest, name
    manifest = json.loads(manifest_path.read_text())
    assert manifest == {"version": 1, "metadata": {"protocol_versions": ["6.0"]}}
    for platform in platforms:
        binary = f"terraform-provider-opper_v{version}"
        if platform.startswith("windows_"):
            binary += ".exe"
        with zipfile.ZipFile(dist / f"{prefix}{version}_{platform}.zip") as archive:
            assert set(archive.namelist()) == {binary, "LICENSE"}, archive.filename
            assert b"Apache License" in archive.read("LICENSE")
            assert archive.getinfo(binary).file_size > 0

    env = os.environ.copy()
    # Ignore personal Terraform CLI configuration, credentials and plugin caches.
    for key in list(env):
        if key.startswith(("TF_", "OPPER_")):
            env.pop(key)
    env.update(TF_IN_AUTOMATION="1", CHECKPOINT_DISABLE="1")
    with tempfile.TemporaryDirectory(prefix="opper-release-install-") as temporary:
        root = Path(temporary)
        platform = json.loads(terraform("version", "-json", cwd=root, env=env))["platform"]
        mirror = root / "mirror" / "registry.terraform.io" / "opper-ai" / "opper"
        mirror.mkdir(parents=True)
        archive = f"{prefix}{version}_{platform}.zip"
        shutil.copy2(dist / archive, mirror / archive)
        config = root / "terraform.rc"
        config.write_text(
            "provider_installation {\n  filesystem_mirror {\n"
            f"    path = {json.dumps(str(root / 'mirror'))}\n"
            '    include = ["registry.terraform.io/opper-ai/opper"]\n'
            "  }\n}\n"
        )
        env["TF_CLI_CONFIG_FILE"] = str(config)
        (root / "main.tf").write_text(
            'terraform {\n  required_providers {\n    opper = {\n'
            '      source = "opper-ai/opper"\n'
            f'      version = "{version}"\n'
            "    }\n  }\n}\n"
        )
        print(terraform("init", "-backend=false", "-input=false", "-no-color", cwd=root, env=env))
        schema = json.loads(terraform("providers", "schema", "-json", cwd=root, env=env))
        provider = schema["provider_schemas"]["registry.terraform.io/opper-ai/opper"]
        assert len(provider["resource_schemas"]) == 11
        assert len(provider["data_source_schemas"]) == 9
        project = provider["resource_schemas"]["opper_project"]["block"]["attributes"]
        initial = project.get("initial_access", {})
        assert initial.get("optional"), "package lacks initial project access support"
        assert {"mode", "role_keys", "user_uuids"} <= set(
            initial.get("nested_type", {}).get("attributes", {})
        ), "package has an incomplete initial access schema"
        key = provider["resource_schemas"]["opper_api_key"]["block"]["attributes"]
        allowed = key.get("allowed_ips", {})
        assert allowed.get("optional") and allowed.get("computed"), "package lacks runtime key allowlists"
        assert allowed.get("type") == ["set", "string"], "package has an invalid allowlist schema"
        installed = json.loads(terraform("version", "-json", cwd=root, env=env))
        assert installed["provider_selections"]["registry.terraform.io/opper-ai/opper"] == version
    print(f"Verified eight packages, manifest, checksums and Terraform installation ({version}).")
    print("Local mirror installation is unsigned; release signing and Registry installation are separate checks.")


if __name__ == "__main__":
    main()
