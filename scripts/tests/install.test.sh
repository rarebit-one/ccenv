#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
root="$(mktemp -d)"
trap 'rm -rf -- "$root"' EXIT

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo 'installer test needs Linux or macOS' >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo 'installer test needs amd64 or arm64' >&2; exit 1 ;;
esac

fixture="$root/fixture"
fakebin="$root/fakebin"
mkdir -p "$fixture" "$fakebin" "$root/bin"
archive="ccenv_0.1.1_${os}_${arch}.tar.gz"
printf 'test ccenv binary\n' > "$fixture/ccenv"
tar -czf "$fixture/$archive" -C "$fixture" ccenv
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$fixture" && sha256sum "$archive" > checksums.txt)
else
  (cd "$fixture" && shasum -a 256 "$archive" > checksums.txt)
fi

cat > "$fakebin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1 $2" == 'release view' ]]; then
  if [[ "$3" == -R ]]; then
    printf 'view\n' >> "$CCENV_TEST_LOG"
    printf 'v0.1.1\n'
  else
    printf 'immutability\n' >> "$CCENV_TEST_LOG"
    printf '%s\n' "${CCENV_TEST_IMMUTABLE:-true}"
  fi
elif [[ "$1 $2" == 'release download' ]]; then
  printf 'download %s\n' "$3" >> "$CCENV_TEST_LOG"
  if [[ "${CCENV_TEST_FAIL_ONCE:-}" == 1 && ! -e "$CCENV_TEST_RETRY_MARKER" ]]; then
    touch "$CCENV_TEST_RETRY_MARKER"
    printf 'simulated transient failure\n' >&2
    exit 1
  fi
  shift 3
  assets=()
  dir=
  while (($#)); do
    case "$1" in
      -R) shift 2 ;;
      -p) assets+=("$2"); shift 2 ;;
      -D) dir=$2; shift 2 ;;
      --clobber) shift ;;
      *) exit 2 ;;
    esac
  done
  for asset in "${assets[@]}"; do
    cp "$CCENV_TEST_FIXTURE/$asset" "$dir/$asset"
  done
  if [[ "${CCENV_TEST_BAD_SUM:-}" == 1 ]]; then
    printf '%064d  %s\n' 0 "${assets[0]}" > "$dir/checksums.txt"
  fi
elif [[ "$1 $2" == 'release verify-asset' ]]; then
  printf 'verify\n' >> "$CCENV_TEST_LOG"
  [[ "${CCENV_TEST_BAD_ATTEST:-}" != 1 ]]
else
  exit 2
fi
EOF
chmod +x "$fakebin/gh"

export CCENV_TEST_FIXTURE="$fixture" CCENV_TEST_LOG="$root/gh.log"
export CCENV_TEST_RETRY_MARKER="$root/retried"
export PATH="$fakebin:$PATH"
installer="$repo_dir/scripts/install.sh"

bash "$installer" --bin-dir "$root/bin" > "$root/output"
cmp "$fixture/ccenv" "$root/bin/ccenv"
[[ "$(cat "$root/gh.log")" == $'view\ndownload v0.1.1\nimmutability\nverify' ]]
[[ "$(stat -c %a "$root/bin/ccenv" 2>/dev/null || stat -f %Lp "$root/bin/ccenv")" == 755 ]]

: > "$root/gh.log"
bash "$installer" --version 0.1.1 --bin-dir "$root/bin" > "$root/output"
[[ "$(cat "$root/gh.log")" == $'download v0.1.1\nimmutability\nverify' ]]

: > "$root/gh.log"
CCENV_TEST_FAIL_ONCE=1 bash "$installer" --bin-dir "$root/bin" > "$root/output" 2>&1
[[ "$(cat "$root/gh.log")" == $'view\ndownload v0.1.1\ndownload v0.1.1\nimmutability\nverify' ]]
cmp "$fixture/ccenv" "$root/bin/ccenv"

: > "$root/gh.log"
CCENV_TEST_IMMUTABLE=false bash "$installer" --bin-dir "$root/bin" > "$root/output" 2>&1
[[ "$(cat "$root/gh.log")" == $'view\ndownload v0.1.1\nimmutability' ]]
grep -q 'checksum verified only' "$root/output"

printf 'existing binary\n' > "$root/bin/ccenv"
if CCENV_TEST_BAD_SUM=1 bash "$installer" --bin-dir "$root/bin" > "$root/output" 2>&1; then
  echo 'checksum mismatch should fail' >&2
  exit 1
fi
[[ "$(cat "$root/bin/ccenv")" == 'existing binary' ]]
grep -q 'checksum mismatch' "$root/output"

if CCENV_TEST_BAD_ATTEST=1 bash "$installer" --bin-dir "$root/bin" > "$root/output" 2>&1; then
  echo 'attestation failure should stop installation' >&2
  exit 1
fi
[[ "$(cat "$root/bin/ccenv")" == 'existing binary' ]]
grep -q 'attestation verification failed' "$root/output"

bad_version="\$(touch $root/pwned)"
if bash "$installer" --version "$bad_version" --bin-dir "$root/bin" > "$root/output" 2>&1; then
  echo 'invalid version should fail' >&2
  exit 1
fi
[[ "$(cat "$root/bin/ccenv")" == 'existing binary' ]]
[[ ! -e "$root/pwned" ]]

echo 'installer tests passed'
