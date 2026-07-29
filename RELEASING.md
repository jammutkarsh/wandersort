> SPDX-License-Identifier: AGPL-3.0-or-later
>
> Copyright (c) 2026 Utkarsh Chourasia

# Releasing wandersort

Releases are automated by [GoReleaser](https://goreleaser.com) plus two extra CI
jobs for the native installers. **To cut a release: push a semver tag.**

```bash
git tag v0.1.0
git push origin v0.1.0
```

That triggers `.github/workflows/release.yml`, which:

1. **goreleaser** (Ubuntu) — builds linux/macOS/Windows × amd64/arm64, creates the
   GitHub release with archives + `checksums.txt`, `.deb`/`.rpm`/`.apk` packages,
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

## winget (not enabled yet)

winget manifest generation is in GoReleaser OSS, but the auto-PR to
`microsoft/winget-pkgs` is a Pro feature. To enable: fork `microsoft/winget-pkgs`,
uncomment the `winget:` block in `.goreleaser.yaml`, and either let GoReleaser push
to your fork (then open the PR manually) or add the community
`vedantmgoyal9/winget-releaser` action to the release workflow.

## Signing (future)

`.pkg` and `.msi` are currently **unsigned** — users must bypass Gatekeeper /
SmartScreen. To fix, add an Apple Developer ID + Windows code-signing cert and move
signing into the `macos-pkg` / `windows-msi` jobs.
