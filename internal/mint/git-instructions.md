# Mint session instructions

## Sandbox and file search

On Windows, never call Copilot's built-in `glob` or `rg` search tools: their
packaged executables are always denied by the sandbox. Do not probe, search for,
or execute their runtime under `%LOCALAPPDATA%\copilot\pkg\`, even once. Use
permitted repository inspection commands such as `git ls-files` and `git grep`
instead; these only cover tracked files by default.

Keep the sandbox enabled. If another operation is explicitly denied, report the
block rather than retrying it through another tool or shell.

## Git authentication

Use the repository in `GH_REPO` for authenticated Git operations. Do not use any
other credential, SSH, or a credential helper. Do not print credentials, add
them to Git config files, or enable HTTP tracing.

For a push, use this pattern in PowerShell:

```powershell
if (-not $env:MINT_GIT_AUTH) { throw 'Mint Git credential is missing' }
$url = "https://github.com/${env:GH_REPO}.git"
$env:MINT_GIT_HEADER = "Authorization: Basic $env:MINT_GIT_AUTH"
try {
    git "--config-env=http.$url.extraHeader=MINT_GIT_HEADER" push $url "HEAD:refs/heads/$branch"
} finally {
    $env:MINT_GIT_HEADER = $null
}
```

In a POSIX shell:

```sh
(
    : "${MINT_GIT_AUTH:?Mint Git credential is missing}"
    url="https://github.com/${GH_REPO}.git"
    export MINT_GIT_HEADER="Authorization: Basic $MINT_GIT_AUTH"
    git "--config-env=http.$url.extraHeader=MINT_GIT_HEADER" push "$url" "HEAD:refs/heads/$branch"
)
```

Set `branch` to the intended remote branch before running the example. Use the same
header and explicit HTTPS URL for authenticated fetches or ref lookups. Leave
ordinary local Git commands unchanged. If a push fails, report it without falling
back to another credential.

## Commands and pull requests

- On Windows, prefer PowerShell 7 (`pwsh.exe`). Use Windows PowerShell 5.1 if
  PowerShell 7 is unavailable and the command is permitted in the sandbox.
  Never switch shells to retry an operation the sandbox denied. On other
  platforms, use the active shell's syntax.
- In PowerShell, avoid complex `--jq` quoting; request JSON with the command's
  `--json` option and parse it with PowerShell when needed.
- Check each command's exit code and output before running dependent commands.
- After pushing, use `gh pr create --head` with the pushed branch so `gh` does not
  attempt another push.
- Pass a full commit object ID to `--match-head-commit`; abbreviated SHAs are rejected.
- Use `gh pr merge --auto` only if the repository permits auto-merge. If GitHub says
  auto-merge is disabled, wait for required checks to pass, then use a regular merge.
