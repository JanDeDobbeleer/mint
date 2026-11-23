# Security

mint starts Copilot in its local sandbox and supplies the fine-grained personal access token stored for the current
repository. The token is user-owned and restricted by its selected repositories and permissions. Copilot
authentication uses a separate credential.

mint stores each repository token encrypted with a passphrase in the user's configuration directory. The token is
decrypted in memory for launch and is not revoked when a session ends. Revoke tokens through GitHub if they are no
longer needed.

## Reporting

Report suspected vulnerabilities privately through the repository's GitHub security advisories. Do not include
tokens, private keys, or credential-bearing logs.
