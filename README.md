<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/mint-logo-dark.svg">
    <img src="assets/mint-logo.svg" alt="Mint" width="200">
  </picture>
</p>

# mint

Launch GitHub Copilot CLI in its local sandbox with a fine-grained personal access token scoped to the current repository.

```powershell
mint
```

Sandboxed `gh` commands use the repository token. Copilot keeps your existing home and settings, and mint does not replace
your normal GitHub CLI login.

## Requirements

- GitHub CLI and Git.
- GitHub Copilot CLI 1.0.112 or later, with a supported local sandbox host.
- A GitHub account with access to the repositories where you intend to use mint.
- A separate credential authorized to use Copilot.

Only GitHub.com is supported.

## Install

On Windows, install the matching `.appinstaller` file from the latest
[GitHub release](https://github.com/JanDeDobbeleer/gh-mint/releases) for automatic updates. You can also install
the signed MSIX package directly:

```powershell
Add-AppxPackage .\mint-windows-amd64.msix
mint setup --help
```

The package installs the `mint.exe` command alias. Choose the file matching your processor architecture.
On macOS, Linux, and FreeBSD, download the matching standalone binary from the same release and place it on `PATH`.
No GitHub CLI extension is installed.

## Configure Mint

Run setup from a GitHub checkout:

```powershell
mint setup
```

Setup saves the remote name and trusted Copilot executable, then applies the [credential policy](#copilot-authentication)
to your normal Copilot home. Run `mint setup` again to reapply that policy. Setup does not replace an existing
configuration; edit it manually to change the remote or executable. On first setup, `--remote` selects a non-default
remote and `--copilot` pins a trusted executable path.

On the first launch in each repository, Mint prints a pre-filled link to create a fine-grained personal access token.
Select **Only select repositories** and choose the repository Mint found from the configured Git remote. The form
pre-fills write access for Contents, Issues, Pull requests, and Workflows; remove any permissions you do not need.
Metadata read access is implicit. Contents write is needed to push, Pull requests write to create PRs, and Workflows
write to push changes to workflow files. Mint validates that GitHub allows the token to read the repository, but
cannot safely verify every write permission without attempting those operations.

On the first launch, create one master password for Mint. The same password unlocks every repository's tokens;
adding another repository does not create another password. Mint asks for it once per launch, then accepts the token
at a hidden prompt when needed. Use a long, unique password. Tokens and passwords are never printed or written to
plain-text files. A wrong master password stops the launch without replacing any credentials.

Mint encrypts a random store key in `master.enc` using your master password. That key protects the separate,
repository-bound token files. Keep `master.enc` with your encrypted tokens; losing it or forgetting the password
requires enrolling your tokens again.

Fine-grained tokens belong to the user who created them. Operations are attributed to that user, and the token can
remain valid after a Mint session ends. Mint checks repository access on each launch. If the token is expired or
revoked, Mint asks you to replace it; other GitHub errors are reported. Revoke tokens in GitHub when you no longer
need them.

Mint does not use or delete GitHub App keys from older configurations. The old `app_id` field is ignored; the old
`key_file` path remains only to deny its directory to Copilot's built-in file tools. Remove the field and old key
yourself once nothing else uses it. Mint uses your normal Copilot home (`COPILOT_HOME` if set, otherwise Copilot's
default); normal launches do not edit its settings.

Mint uses a compact, numbered terminal interface with a green accent and hidden secret prompts. Styling is built
in, with no added dependencies. Redirected output, `NO_COLOR`, `CLICOLOR=0`, and `TERM=dumb` use plain text.

Configuration and encrypted per-repository tokens stay in the OS user configuration directory. Tokens are stored
under its `tokens` subdirectory:

- Windows: `%APPDATA%\gh-mint\config.json`
- Linux: `$XDG_CONFIG_HOME/gh-mint/config.json`, or `~/.config/gh-mint/config.json`
- macOS: `~/Library/Application Support/gh-mint/config.json`

On Windows, Go's `0600` file mode does not replace NTFS ACLs. Restrict access to Mint's configuration directory.

## Copilot authentication

mint uses `COPILOT_GITHUB_TOKEN` for Copilot itself, falling back to `gh auth token --hostname github.com`.
Sandboxed `gh` commands use the repository PAT supplied as `GH_TOKEN`. Mint disables automatic Git authentication
and supplies a separately masked HTTPS credential in `MINT_GIT_AUTH`. Mint puts `mint-git.instructions.md` in a temporary
session directory and adds it to `COPILOT_CUSTOM_INSTRUCTIONS_DIRS`. The instructions show Copilot how to use
`git --config-env` against the scoped repository and how to open a PR after pushing. The file contains no secrets.
This guides the agent; it does not change how plain `git push` works or rewrite Git remotes.

`mint setup` applies the policy below to your existing Copilot home, or you can merge it into `settings.json` yourself.
It applies to sandboxed Copilot sessions using that home. Setup leaves tool grants unchanged and does not enable
sandboxing for ordinary Copilot launches. Mint checks the policy before loading a repository token.

```json
{
  "sandbox": {
    "allowBypass": false,
    "auth": { "gh": false, "git": false },
    "credentials": {
      "envVars": {
        "GH_TOKEN": {
          "injectHosts": ["github.com", "api.github.com", "uploads.github.com"]
        },
        "MINT_GIT_AUTH": {
          "injectHosts": ["github.com"]
        }
      }
    }
  }
}
```

On Windows, `mint setup` enables **Allow local network** when needed for the credential proxy. If configuring
manually, enable it in `/sandbox config`.

### Allow tools, caches, and dependency sources

Use `/sandbox config` or the `sandbox.userPolicy` section of your Copilot `settings.json`. With automatic
developer-tool access turned off, grant runtime directories read-only and build or download caches read/write.
For Go, find the actual locations with `go env GOROOT GOCACHE GOMODCACHE`. For PowerShell 7 on Windows, put the
standalone install ahead of the Store alias on `PATH` before starting mint.

For example, merge these Windows path grants into your existing settings and adjust the paths as needed:

```json
{
  "sandbox": {
    "allowDevToolAccess": false,
    "userPolicy": {
      "filesystem": {
        "readonlyPaths": ["C:\\Program Files\\Go", "C:\\Program Files\\PowerShell\\7"],
        "readwritePaths": ["$LOCALAPPDATA\\go-build", "$USERPROFILE\\go\\pkg\\mod"]
      }
    }
  }
}
```

Grant Git and GitHub CLI's install directories too if you use them. If you use `network.allowedHosts`, include
the GitHub and package download hosts you need there, not in `GH_TOKEN.injectHosts`. Use `/sandbox policy go test`
or `/sandbox policy pwsh` to check the resulting grants.

## Launch

Run inside a Git working tree:

```powershell
mint
mint --model YOUR_MODEL
mint -p "Explain this repository" -s
```

Except for the `setup`, `creds` and `config` commands, arguments after `mint` are passed to Copilot unchanged, including `--help`,
`--version`, and `--`. mint rejects flags that disable sandboxing or override secret filtering. Arguments are passed
without a shell. Copilot starts at the checkout root by default, including
when you invoke mint from a subdirectory. A non-default or ambiguous remote is never silently selected.

### Lifetime and cleanup

The PAT's expiration is chosen during creation and may be limited by organization policy. Mint neither refreshes nor
revokes it. It remains encrypted in Mint's configuration until you revoke it in GitHub or replace its local copy.
Temporary Git and GitHub CLI configuration is removed on handled exit. Copilot keeps session history in your normal
home; launch through Mint again when resuming so credentials are revalidated for the current remote.

## Credentials

Mint keeps named credentials in the same encrypted store as repository tokens, under the same master password:

```powershell
mint creds add copilot-token
mint creds add webhook-secret --password-stdin   # master password from the first line of standard input
mint creds list
mint creds rm webhook-secret
```

Values are typed at a hidden prompt and are never accepted as flags or arguments, which would leak into process lists
and shell history; Mint rejects such options without echoing them. `--password-stdin` only supplies the master
password. `mint creds list` shows names and last-changed times, never values, and needs no password. Each credential
file is bound to its name, so copying one file over another fails to decrypt. `mint creds --help` lists the supported
names. Nothing reads these credentials yet; the daemon milestones build on them.

## Development

Use Go 1.27.4 or later. Install the pinned analysis tools:

```powershell
go install golang.org/x/tools/go/analysis/passes/modernize/cmd/modernize@v0.52.0 golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.52.0
```

Run the checks:

```powershell
pwsh -NoProfile -File .\.github\scripts\test-release-version.ps1
modernize ./...
fieldalignment ./...
go test ./...
go vet ./...
golangci-lint run
go build -trimpath -o mint.exe .
```

Tests use local HTTP servers and helper subprocesses. They verify repository-token validation and encryption,
redirect refusal, argument/environment forwarding, configuration-directory guardrails, and exit-status propagation
without live GitHub credentials.

CI checks Windows, Linux, and macOS. Every push to protected `main` automatically builds standalone macOS, Linux, and FreeBSD
binaries, architecture-specific Windows MSIX packages, and matching AppInstaller manifests. Windows executables
and MSIX packages are Authenticode signed with Azure Artifact Signing using the existing release signing setup.
Signatures must be valid and timestamped before publication. Other platform binaries are not Authenticode signed.
GitHub build provenance attestations cover the final release assets.

Before merging a release workflow change, configure the `AZURE_TENANT_ID`, `AZURE_CLIENT_ID`, and `AZURE_CLIENT_SECRET`
secrets in the protected GitHub `release` environment using the existing oh-my-posh signing identity. Remove any
repository-level copies: otherwise a different workflow could bypass the environment's approval gate.
That identity needs the Artifact Signing Certificate Profile Signer role for the configured profile in the signing account at
`https://weu.codesigning.azure.net`. Missing credentials or failed signing abort the release; unsigned Windows
executables or MSIX packages are never published as a fallback. The MSIX publisher in its manifest is
`CN=Jan Joris De Dobbeleer, O=Jan Joris De Dobbeleer, L=Diest, C=BE`.

Keep the `release` environment restricted to the `main` branch so workflows running from other branches or tags
cannot access signing credentials. Main's required pull request checks are the release gate; the release workflow
does not rerun tests. Conventional commits determine the next version: breaking changes increase the major version,
features increase the minor version, and other changes increase the patch version.
Version calculation uses the repository's PowerShell script, not a third-party action. It selects the highest
reachable stable `vMAJOR.MINOR.PATCH` tag, ignores prerelease and noncanonical tags, and reuses a tag already on the
current commit. A repository without a stable tag starts at `v0.1.0`. Complete Git history is required.

Versioning and build/sign jobs have only `contents: read`, and checkouts never retain Git credentials. A separate
publication job receives signed binaries by immutable artifact ID; it alone has repository-write, OIDC, and attestation
permissions, and it has no Azure credentials or source checkout. Release tags are created for the exact merged commit.
Release tags are created automatically; only administrators may update or delete them. Tag pushes do not trigger
release jobs. Repository administrators remain trusted to manage these controls.

Workflow actions are pinned to commit SHAs.

Use [Conventional Commits](https://www.conventionalcommits.org/) for commit messages, for example
`feat: add a launcher option` or `fix(config): validate the remote`. CI checks every commit in pull requests
and pushes to `main`, not pull request titles.

## License

MIT.
