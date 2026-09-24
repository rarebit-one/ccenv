#!/usr/bin/env bash
set -euo pipefail

repo=rarebit-one/ccenv
bin_dir="${HOME:?}/.local/bin"
requested_version=
tmpdir=
staged=

usage() {
  cat <<'EOF'
Usage: bash install.sh [--version vX.Y.Z] [--bin-dir DIR]

Install the latest ccenv release into ~/.local/bin, or update an existing copy.
Requires GitHub CLI access to the private rarebit-one/ccenv repository.
EOF
}

fail() {
  printf 'ccenv installer: %s\n' "$*" >&2
  exit 1
}

cleanup() {
  if [[ -n "$staged" ]]; then
    rm -f -- "$staged"
  fi
  if [[ -n "$tmpdir" ]]; then
    rm -rf -- "$tmpdir"
  fi
}
trap cleanup EXIT

while (($#)); do
  case "$1" in
    --version|--bin-dir)
      (($# >= 2)) || fail "$1 requires a value"
      if [[ "$1" == --version ]]; then
        requested_version=$2
      else
        bin_dir=$2
      fi
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown option: $1"
      ;;
  esac
done

[[ -n "$bin_dir" ]] || fail 'install directory is empty'
[[ "$(id -u)" != 0 ]] || fail 'run without sudo as your normal user'
for tool in gh tar install mktemp awk mv; do
  command -v "$tool" >/dev/null 2>&1 || fail "$tool is required"
done

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail 'only Linux and macOS releases are available' ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) fail 'only amd64 and arm64 releases are available' ;;
esac

if [[ -n "$requested_version" ]]; then
  tag="v${requested_version#v}"
else
  tag="$(gh release view -R "$repo" --json tagName --jq '.tagName')" ||
    fail 'cannot read the latest release; check gh auth status and repository access'
fi
[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] ||
  fail "invalid release version: $tag"

archive="ccenv_${tag#v}_${os}_${arch}.tar.gz"
umask 077
tmpdir="$(mktemp -d)" || fail 'cannot create a temporary directory'
downloaded=false
for attempt in 1 2 3; do
  if gh release download "$tag" -R "$repo" -p "$archive" -p checksums.txt \
    -D "$tmpdir" --clobber > "$tmpdir/download.log" 2>&1; then
    downloaded=true
    break
  fi
  if ((attempt < 3)); then
    printf 'ccenv installer: download failed; retrying (%d/3)\n' "$attempt" >&2
    sleep "$attempt"
  fi
done
[[ "$downloaded" == true ]] ||
  fail "cannot download $archive from $tag after 3 attempts; check gh auth status and network access"
[[ -f "$tmpdir/$archive" && -f "$tmpdir/checksums.txt" ]] ||
  fail 'the release is missing the archive or checksums'

expected="$(awk -v name="$archive" '$2 == name { print $1 }' "$tmpdir/checksums.txt")"
[[ "$expected" =~ ^[[:xdigit:]]{64}$ ]] || fail "missing or invalid checksum for $archive"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmpdir/$archive" | awk '{ print $1 }')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$tmpdir/$archive" | awk '{ print $1 }')"
else
  fail 'sha256sum or shasum is required to verify the download'
fi
[[ "$actual" == "$expected" ]] || fail "checksum mismatch for $archive"

immutable="$(gh release view "$tag" -R "$repo" --json isImmutable --jq '.isImmutable' \
  2> "$tmpdir/release.log")" || fail "cannot verify release metadata for $tag"
case "$immutable" in
  true)
    gh release verify-asset "$tag" "$tmpdir/$archive" -R "$repo" \
      > "$tmpdir/attestation.log" 2>&1 ||
      fail "release attestation verification failed for $archive; update gh and check the release"
    ;;
  false)
    printf 'ccenv installer: %s predates immutable releases; checksum verified only\n' "$tag" >&2
    ;;
  *) fail "unexpected release immutability status for $tag" ;;
esac

tar -xzOf "$tmpdir/$archive" ccenv > "$tmpdir/ccenv" ||
  fail "cannot extract ccenv from $archive"
[[ -s "$tmpdir/ccenv" ]] || fail "the archive contains an empty ccenv binary"

mkdir -p -- "$bin_dir"
staged="$(mktemp "$bin_dir/.ccenv.XXXXXXXX")" || fail "cannot write to $bin_dir"
install -m 755 "$tmpdir/ccenv" "$staged"
mv -f -- "$staged" "$bin_dir/ccenv"
staged=
printf 'Installed ccenv %s to %s/ccenv\n' "${tag#v}" "$bin_dir"
