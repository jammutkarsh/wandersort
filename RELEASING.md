# Releasing wandersort

Releases are automated by [GoReleaser](https://goreleaser.com) plus two extra CI
jobs for the native installers. **To cut a release: push a semver tag.**

```bash
git tag v0.1.0
git push origin v0.1.0
```

That triggers `.github/workflows/release.yml`, which:

1. **goreleaser** (Ubuntu) — builds linux/macOS/Windows × amd64/arm64, creates the
   GitHub release with archives, bare binaries (used by the install scripts) + `checksums.txt`, `.deb`/`.rpm`/`.apk` packages,
   and pushes the Homebrew cask + Scoop manifest to the tap repos.
2. **macos-pkg** (macOS) — builds unsigned `.pkg` installers via `pkgbuild` and
   uploads them to the release.
3. **windows-msi** (Windows) — builds an unsigned `.msi` via WiX and uploads it.

## One-time setup

1. **Tap repos** (already created): `jammutkarsh/homebrew-tap`,
   `jammutkarsh/scoop-bucket`.
2. **`TAP_GITHUB_TOKEN` secret** — GoReleaser needs a token that can push to those
   *other* repos (the default `GITHUB_TOKEN` can't). Create a PAT with
   `contents:write` on both tap repos (fine-grained) or classic `repo` scope, then:

   ```bash
   gh secret set TAP_GITHUB_TOKEN --repo jammutkarsh/wandersort
   ```

## Test before tagging

```bash
# Dry run — builds everything into ./dist without publishing.
goreleaser release --snapshot --clean
./dist/wandersort_darwin_arm64_v1/wandersort --version   # confirms ldflags
```

For a real end-to-end test, push a prerelease tag (e.g. `v0.1.0-rc.1`) and check
the release assets, `brew install`, and `scoop install` before cutting `v0.1.0`.

## winget

Each release pushes the manifest to a `wandersort-<version>` branch on the
`jammutkarsh/winget-pkgs` fork and opens a PR to `microsoft/winget-pkgs`.
`TAP_GITHUB_TOKEN` must be a classic token with `public_repo` scope: a
fine-grained one can't open a PR on a repo you don't own. A failed PR is logged
but doesn't fail the release. The first PR needs the Microsoft CLA signed and a
moderator's review before `winget install jammutkarsh.wandersort` works.

## Signing (future)

`.pkg` and `.msi` are currently **unsigned** — users must bypass Gatekeeper /
SmartScreen. To fix, add an Apple Developer ID + Windows code-signing cert and move
signing into the `macos-pkg` / `windows-msi` jobs.
