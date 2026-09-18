# Packaging and Homebrew

The repository doubles as a custom Homebrew tap. Once this code is pushed to the
public repository, install the development version on macOS or Linux:

```sh
brew tap paramon-tech/tuigram https://github.com/paramon-tech/tuigram
brew install --HEAD paramon-tech/tuigram/tuigram
tuigram --demo
```

`Formula/tuigram.rb` builds the current `main` branch with Go. It is intentionally
a HEAD formula until a versioned release is published. This terminal application
uses a formula rather than a GUI application cask.

Build a release archive locally from the repository root:

```sh
bash scripts/package.sh darwin arm64 v0.1.0
bash scripts/package.sh linux amd64 v0.1.0
```

The script accepts `linux`, `freebsd`, `openbsd`, or `darwin`, each with `amd64`
or `arm64`. Archives contain the binary, README, security/development guides, and MIT license in one versioned
directory. Builds disable CGO and embed version, commit, and commit date.

Pushing a semantic version tag such as `v0.1.0` runs the release workflow. It
checks the application, builds all eight archives, generates `SHA256SUMS`, and
creates a **draft** GitHub release. Review the draft and publish it in GitHub.
Existing published releases are never overwritten by the workflow. No release
or tap has been published merely by adding these files.

After downloading the archives and `SHA256SUMS` from a published release, verify
the downloaded archive's matching checksum before extracting it:

```sh
# Linux and BSD with sha256sum installed:
sha256sum --ignore-missing --check SHA256SUMS
# macOS, when all release archives have been downloaded:
shasum -a 256 --check SHA256SUMS
```

Only Linux and macOS runners execute tests in CI. BSD binaries are cross-compiled;
runtime testing on FreeBSD and OpenBSD remains a release qualification step.
