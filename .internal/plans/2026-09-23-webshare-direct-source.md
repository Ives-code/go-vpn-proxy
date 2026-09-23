# Webshare direct source implementation plan

1. Add parser tests for valid four-field lines and malformed all-or-nothing input.
   Implement normalized sing-box HTTP nodes with anonymous stable IDs.
2. Add a private regular-file fetch path and tests for source match, symlinks,
   size limits, and last-known-good behavior.
3. Add a `webshare_file` YAML setting and wire the seventh source into the
   existing Manager, shared Registry, status, and alert labels. Test per-source
   counts and independent listener routing.
4. Run the race suite, vet, security checks and Linux build. Copy the secret file
   with root/dual-egress 0640 permissions to 117, update service config, restart,
   wait for probes, and check both proxy listeners, Webshare counts, and port
   ownership. Commit and publish code without the private file.
