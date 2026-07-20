# npm Wrapper Smoke Checklist

Run this checklist when a PR changes npm distribution files such as
`package.json`, `bin/kajicode.js`, `scripts/npm/build-platform-packages.mjs`,
Go build or release tooling, or the npm `bin` wrapper.

## Required Checks

```bash
go test ./internal/npmwrapper ./internal/release
go run ./cmd/kajicode-release build
go run ./cmd/kajicode-release smoke
```

Also run the Go checks when the PR changes Go entrypoint, CLI, or release
artifact behavior:

```bash
go test ./...
go run ./cmd/kajicode-release build
go run ./cmd/kajicode-release smoke
```

## End-To-End Assembly Smoke

Build a real archive for the host platform and assemble the npm payloads from
it (this is exactly what the publish job does):

```bash
go run ./cmd/kajicode-release package
node scripts/npm/build-platform-packages.mjs \
  --artifacts-dir dist/release --out-dir dist/npm --only <platform>-<arch>
```

Then simulate an installed layout and run the wrapper through each resolution
path:

```bash
# platform-package path
mkdir -p /tmp/kajicode-sim/node_modules/@dishant0406
cp -R dist/npm/wrapper /tmp/kajicode-sim/node_modules/@dishant0406/kajicode
cp -R dist/npm/platforms/kajicode-<platform>-<arch> \
  "/tmp/kajicode-sim/node_modules/@dishant0406/kajicode-<platform>-<arch>"
node /tmp/kajicode-sim/node_modules/@dishant0406/kajicode/bin/kajicode.js --version

# first-run download fallback (delete the platform package first)
rm -rf "/tmp/kajicode-sim/node_modules/@dishant0406/kajicode-<platform>-<arch>"
node /tmp/kajicode-sim/node_modules/@dishant0406/kajicode/bin/kajicode.js --version
```

## Checklist

- `package.json` has the expected package name (`@dishant0406/kajicode`), version,
  `bin.kajicode` entry, and NO `scripts` entries — the published package must be
  free of lifecycle scripts (see NPM_PACKAGING.md).
- `scripts/npm/build-platform-packages.mjs` emits platform payloads whose
  `package.json` is `@dishant0406/kajicode@<version>-<platform>-<arch>` with matching
  `os`/`cpu`, contains the executable binary and the vendored `helpers/` tree
  with regular-file (non-symlink) `.bin` shims, and a wrapper payload with no
  scripts, no dependencies, and the full five-alias `optionalDependencies`
  matrix.
- `scripts/postinstall.mjs` (the first-run fallback) resolves the correct
  release asset name/URL per platform (`KAJICODE_INSTALL_DRY_RUN=1` prints the
  plan), verifies the downloaded archive's SHA-256, and extracts only the
  known binary basenames into place. `KAJICODE_SKIP_DOWNLOAD=1` opts out cleanly
  (exit 0) and an unsupported platform/arch is a non-fatal skip.
- The wrapper prefers the platform package binary over a previously downloaded
  one, and `agent-browser`/`tuistory` run from the platform package's
  `helpers/node_modules/.bin` shims.
- The built binary exits 0 for `kajicode --version` or `kajicode --help`.
- `kajicode --version` reports `kajicode <package.json version>`.
- Release packaging still emits the expected archive and checksum names when
  package release files change.
