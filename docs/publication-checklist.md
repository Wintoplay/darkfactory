# Publication checklist

This is a preparation checklist. It does not authorize publication, rewriting
history, deleting private files, or rotating credentials on its own.

- [ ] Confirm ownership and permission to publish all first-party work. Keep
      third-party notices and licenses, including the Orca reference files.
- [ ] Confirm the MIT copyright notice and the nonbinding AI-training request.
- [ ] Review tracked files and a dedicated secret scan of every history ref.
      Scan release assets, CI logs, archives, and Git LFS separately if used.
      Treat scan results as leads, not proof of absence.
- [ ] Review screenshots and QR codes, logs, paths, chat samples, commit author
      metadata, hostnames, and test artifacts for private information.
- [ ] Revoke and rotate any exposed credential. Removing a secret from Git does
      not revoke it or remove copies that already left the repository.
- [ ] Decide whether to publish existing history or create a clean snapshot.
      Before rewriting, make and verify a private backup with all refs and tags.
      Audit the public snapshot and its metadata again. Keep the private backup
      outside the publication tree and protect it as sensitive data.
- [ ] Check repository visibility, existing releases, Actions logs, forks, and
      caches separately. A fresh history does not recall existing copies.
- [ ] Provide a private vulnerability-reporting channel before public launch.
      Do not advertise an address or response commitment until it exists.
- [ ] Publish the threat model and accurately state unresolved security and
      device-acceptance limits. Do not call the product independently audited.
- [ ] Verify license notices accompany all distributed source and binary
      packages, and review artifact provenance and signing independently.
