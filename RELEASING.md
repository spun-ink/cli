# Releasing

A release is a pushed `v*` tag on `main`. The release workflow does the rest; nobody builds or
uploads anything by hand.

## Cut a release

1. `main` is green in CI (Linux, macOS, Windows, `govulncheck`, the GoReleaser snapshot).
2. Pick the version with semver: a fix is a patch, a feature a minor. A tag with a suffix
   (`v0.3.0-rc.1`) becomes a GitHub prerelease: `spun upgrade` and the installers skip it unless
   it is named.
3. Tag the commit on `main` and push the tag:

   ```bash
   git fetch origin
   git tag -a v0.3.0 -m "spun v0.3.0" origin/main
   git push origin v0.3.0
   ```

4. Watch the `release` workflow (`gh run watch`). It has two jobs:
   - **publish** runs the tests, then GoReleaser: six archives, `checksums.txt`, an SBOM per
     archive, and the release notes (the install header from `.goreleaser.yaml` plus GitHub's
     generated changelog).
   - **smoke** installs the new release with `install.sh` / `install.ps1` on Linux, macOS and
     Windows, and checks that `spun --version` prints exactly `spun version X (release)`. It also
     runs the upgrade leg (below).
5. When both jobs are green, the release is out. Every installed release picks it up through the
   update check within a day, and `spun upgrade` installs it.

A security fix is a release like any other. Its notes say what it fixes (see [SECURITY.md](SECURITY.md)).

## The upgrade leg

The smoke job also installs the **previous release** and runs `spun upgrade <new tag>` with it.
That tests the way most users will get the release: through the updater of the version they
already have. The fresh install above cannot catch a broken updater.

Nothing needs setting. The publish job picks the previous release from the git tags: the newest
earlier tag in the new tag's history that is not a prerelease. It skips the leg when that release
is older than `v0.2.0`, the first release with `spun upgrade`.

Hosted Windows runners run as administrator, and `spun upgrade` refuses that by design (exit 5,
`upgrade_required`). The Windows leg therefore records a warning instead of upgrading. That
warning is expected. The Windows swap itself is covered by the unit tests in CI's Windows job.

## If a release goes wrong

- **publish fails:** nothing was released. Fix it on `main` through a pull request, delete the tag
  (`git push origin :v0.3.0` and `git tag -d v0.3.0`), and tag again.
- **smoke fails:** the release is public. Do not delete it: users may already have it. Fix forward
  with a patch release. If the release is dangerous, mark it as a prerelease on GitHub so that
  `releases/latest` points back at the previous one. `spun upgrade` and the update check then stop
  offering it.
