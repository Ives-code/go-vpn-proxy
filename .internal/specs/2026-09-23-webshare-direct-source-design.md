# Webshare direct proxy source

Goal: add the operator's 100 authenticated HTTP proxies to the existing shared
registry behind both HTTP proxy listeners, with the same probe and ejection behavior
as subscription nodes.

The local input is a private `IPv4:port:username:password` text file. Deployment
copies it to `/etc/dual-egress-gateway/webshare-proxies.txt` (root-owned, readable
by `dual-egress`, never committed). A YAML `webshare_file` path adds one file source
after the configured HTTPS subscriptions. The existing refresh loop reads it at
startup and every 30 minutes. The existing HTTPS probe runs on every node every
30 seconds and the shared registry ejects failing nodes from both listeners;
subsequent probes can reinstate them. Existing connections stay pinned.

Parse the file atomically, accepting only nonempty four-field IPv4 records with
valid ports and nonempty credentials. Fail the source as a whole on malformed
records, keeping its last successful in-memory snapshot. The file is a bounded,
non-symlink regular file; no raw credentials or file path are exposed in API/logs.
Normalize entries to sing-box HTTP outbounds using the current engine, preserving
their upstream Basic authentication. Do not publish the file in Git or Docker.

The status and alert labels identify the new source as `Webshare file`; its node
counts appear next to subscription counts without changing the unique global total.
Verification covers parser success and rejection, file privacy and size bounds,
engine dialing through an authenticated HTTP proxy, configuration and status,
full race tests, a Linux build, and a live check on 117 after deployment.
