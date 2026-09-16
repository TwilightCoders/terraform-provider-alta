# Releasing

The Terraform Registry serves a provider version only if it can verify a detached
signature over that release's checksum file, made with a GPG key registered to the
publishing namespace. The signing is done by the release workflow, not by hand.

## The signing key is per namespace, not per provider

One key belongs to the `TwilightCoders` namespace and signs every provider published under
it. Registering it again for each new provider is not necessary — the registry asks which
of the namespace's keys to use when a provider is first published.

Generate it once:

```bash
gpg --full-generate-key
# (1) RSA and RSA, 4096 bits, an expiry you will actually renew (2y is reasonable)
# Real name: TwilightCoders
# Email:     dale@twilightcoders.net

gpg --list-secret-keys --keyid-format=long   # note the fingerprint
```

RSA rather than Ed25519: the registry documents RSA, and ECC keys have a history of being
rejected by parts of the publishing flow. Not worth discovering at release time.

Then:

1. **Registry** — paste the public key into the organisation's signing keys:
   `gpg --armor --export <FINGERPRINT>`
2. **CI** — the armoured private key and its passphrase become organisation secrets named
   `GPG_PRIVATE_KEY` and `PASSPHRASE`, scoped to the provider repositories:
   `gpg --armor --export-secret-keys <FINGERPRINT>`
   Organisation secrets mean one copy for every provider rather than one copy per
   repository, and one place to rotate.
3. **Backup** — the private key and a revocation certificate belong in 1Password.
   `gpg --output revoke.asc --gen-revoke <FINGERPRINT>`

Rotating the key later means registering the new public key and keeping the old one until
no published version is still verified against it.

## Cutting a release

```bash
make release VERSION=0.2.0   # updates CHANGELOG, commits, tags — does not push
git push origin main --follow-tags
```

The tag triggers `.github/workflows/release.yml`, which builds each platform's zip, writes
`terraform-provider-alta_<version>_SHA256SUMS`, signs it with the imported key, and
publishes the GitHub release the registry then ingests.

## Versioning while the schema moves

Breaking changes below 1.0 go in the minor version, because that is what Terraform's lock
file and the registry both treat as a real change. Reusing a version — particularly
rebuilding one already installed in a shared plugin mirror — produces a checksum mismatch
that reads like tampering rather than an upgrade, and costs whoever hits it an
investigation.

Anything that removes or renames an attribute, or changes an import id's shape, is
breaking. Say so in the changelog entry, in those words.
