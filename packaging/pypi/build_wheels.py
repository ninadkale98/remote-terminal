"""Wrap the prebuilt rterm binaries in platform wheels for PyPI.

`pip install remote-terminal` then puts the `rterm` binary on PATH, the same
way tools like ruff and uv ship native binaries through PyPI. Each wheel holds
one binary under <name>.data/scripts/, which pip installs into its bin
(or Scripts) directory.

Usage: python3 packaging/pypi/build_wheels.py VERSION DIST_DIR OUT_DIR
  DIST_DIR has rterm-<os>-<arch>[.exe] files, as built by the release workflow.
"""
import base64
import hashlib
import pathlib
import sys
import zipfile

NAME = "remote-terminal"
DIST_NAME = NAME.replace("-", "_")
REPO = "https://github.com/ninadkale98/remote-terminal"

# binary -> wheel platform tags. The binaries are static (CGO_ENABLED=0), so
# one Linux build covers both glibc (manylinux) and musl (Alpine) systems.
# Go 1.24 needs macOS 11 or later.
PLATFORMS = {
    "rterm-linux-amd64": ["manylinux_2_17_x86_64", "manylinux2014_x86_64", "musllinux_1_1_x86_64"],
    "rterm-linux-arm64": ["manylinux_2_17_aarch64", "manylinux2014_aarch64", "musllinux_1_1_aarch64"],
    "rterm-darwin-amd64": ["macosx_11_0_x86_64"],
    "rterm-darwin-arm64": ["macosx_11_0_arm64"],
    "rterm-windows-amd64.exe": ["win_amd64"],
    "rterm-windows-arm64.exe": ["win_arm64"],
}

DESCRIPTION = f"""\
# rterm

A persistent, shared terminal on another machine, for Claude (or any agent)
and you at the same time, over plain SSH.

Claude runs on machine 1 and calls `rterm m2 run "…"` from its normal Bash
tool. The command is typed into a long-lived shell on machine 2 (PowerShell on
Windows, bash on macOS and Linux), and Claude gets the output and exit code
back. You watch the same shell live with `rterm watch m2`.

This package installs the `rterm` command. It is a self-contained native
binary; Python is only used to install it.

## Quick start

```sh
pip install {NAME}        # or: pipx install {NAME} / uv tool install {NAME}

# on machine 2 (Windows: in an Administrator PowerShell)
rterm host init

# on machine 1, paste the pairing line that host init printed, then:
rterm watch <name>
rterm <name> run "hostname"
```

Install the same version on both machines.

Full documentation, diagrams and a demo: {REPO}
"""


def b64hash(data: bytes) -> str:
    return "sha256=" + base64.urlsafe_b64encode(hashlib.sha256(data).digest()).rstrip(b"=").decode()


def build(version: str, binary: pathlib.Path, tags: list, out: pathlib.Path) -> pathlib.Path:
    exe = "rterm.exe" if binary.name.endswith(".exe") else "rterm"
    wheel_name = f"{DIST_NAME}-{version}-py3-none-{'.'.join(tags)}.whl"
    info = f"{DIST_NAME}-{version}.dist-info"
    data = f"{DIST_NAME}-{version}.data"

    metadata = "\n".join([
        "Metadata-Version: 2.1",
        f"Name: {NAME}",
        f"Version: {version}",
        "Summary: A persistent, shared terminal on another machine for Claude and you, over plain SSH",
        "Keywords: ssh,terminal,remote,claude,agent,powershell,windows",
        f"Project-URL: Homepage, {REPO}",
        f"Project-URL: Source, {REPO}",
        f"Project-URL: Issues, {REPO}/issues",
        "Classifier: Environment :: Console",
        "Classifier: Operating System :: Microsoft :: Windows",
        "Classifier: Operating System :: MacOS",
        "Classifier: Operating System :: POSIX :: Linux",
        "Classifier: Topic :: System :: Systems Administration",
        "Classifier: Topic :: Terminals",
        "Description-Content-Type: text/markdown",
        "",
        DESCRIPTION,
    ])
    wheel = "\n".join(
        ["Wheel-Version: 1.0", "Generator: rterm build_wheels.py", "Root-Is-Purelib: false"]
        + [f"Tag: py3-none-{t}" for t in tags]
    ) + "\n"

    files = [
        (f"{data}/scripts/{exe}", binary.read_bytes(), 0o755),
        (f"{info}/METADATA", metadata.encode(), 0o644),
        (f"{info}/WHEEL", wheel.encode(), 0o644),
    ]
    record = "".join(f"{path},{b64hash(blob)},{len(blob)}\n" for path, blob, _ in files)
    record += f"{info}/RECORD,,\n"
    files.append((f"{info}/RECORD", record.encode(), 0o644))

    path = out / wheel_name
    with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_DEFLATED) as zf:
        for name, blob, mode in files:
            zi = zipfile.ZipInfo(name, date_time=(2026, 1, 1, 0, 0, 0))
            zi.external_attr = (0o100000 | mode) << 16  # regular file + permissions
            zi.compress_type = zipfile.ZIP_DEFLATED
            zf.writestr(zi, blob)
    return path


def main() -> None:
    if len(sys.argv) != 4:
        sys.exit(__doc__)
    version = sys.argv[1].removeprefix("v")
    dist, out = pathlib.Path(sys.argv[2]), pathlib.Path(sys.argv[3])
    out.mkdir(parents=True, exist_ok=True)
    for name, tags in PLATFORMS.items():
        binary = dist / name
        if not binary.exists():
            sys.exit(f"missing {binary}")
        print("built", build(version, binary, tags, out).name)


if __name__ == "__main__":
    main()
