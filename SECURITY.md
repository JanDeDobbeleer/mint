# Security

mint starts Copilot in its local sandbox and supplies the fine-grained personal access token stored for the current
repository. The token is user-owned and restricted by its selected repositories and permissions. Copilot
authentication uses a separate credential.

mint stores repository tokens encrypted in the user's configuration directory. One master password protects an
encrypted store key shared by all repository token files. Tokens are decrypted in memory for launch and are not
revoked when a session ends. Revoke tokens through GitHub if they are no longer needed.

## Reporting

Report suspected vulnerabilities privately through the repository's GitHub security advisories. Do not include
tokens, private keys, or credential-bearing logs.
