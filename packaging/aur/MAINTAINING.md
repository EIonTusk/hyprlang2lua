# Maintaining the AUR package

The PKGBUILD and `.SRCINFO` in this directory are the canonical source for
the `hyprlang2lua` AUR package. Upstream releases are cut by tagging the
git repo (`git tag -a vX.Y.Z -m "vX.Y.Z" && git push origin vX.Y.Z`); the
AUR package is then refreshed by pushing an updated PKGBUILD to the AUR
git remote.

## Release flow

1. **Bump the version.** Update `pkgver` (and reset `pkgrel=1`) in `PKGBUILD`.
2. **Refresh the checksum.** From this directory:
   ```sh
   updpkgsums              # rewrites sha256sums= with the real hash for the new tag
   ```
   This requires the tag to already exist on GitHub — GitHub generates
   `archive/vX.Y.Z.tar.gz` automatically once you push the tag.
3. **Regenerate `.SRCINFO`.** AUR requires it to stay in lockstep with the
   PKGBUILD:
   ```sh
   makepkg --printsrcinfo > .SRCINFO
   ```
4. **Smoke-test the build locally.** Catches PKGBUILD breakage before
   pushing to AUR (where it'd surface as user-facing install errors):
   ```sh
   makepkg -f
   namcap PKGBUILD ./*.pkg.tar.zst   # optional lint
   ```
5. **Push to the AUR.** First time only:
   ```sh
   git clone ssh://aur@aur.archlinux.org/hyprlang2lua.git aur-hyprlang2lua
   cp PKGBUILD .SRCINFO aur-hyprlang2lua/
   cd aur-hyprlang2lua && git add . && git commit -m "v$NEW_VERSION" && git push
   ```
   On subsequent releases, just pull the AUR repo and `cp` the new files.

## Notes

- `checkdepends=('lua')` is intentional: the golden tests run an optional
  `luac -p` syntax gate over every generated `.lua` golden (see
  `internal/converter/golden_test.go`). Without lua the tests still pass
  but the gate is silently skipped — better to have it.
- The AUR account's SSH public key has to be registered at
  https://aur.archlinux.org/account before the first push. The same key
  used for GitHub works; add an `aur.archlinux.org` Host entry in
  `~/.ssh/config` if your default identity is something else.
- A separate `hyprlang2lua-bin` PKGBUILD (downloading a prebuilt binary
  from a GitHub release) is *not* maintained here — the source build is
  fast (single Go binary, single dep) and a bin variant is mostly a
  release-artifact maintenance burden.
