# Internal CA

The internal CA gives the machines in your network trusted HTTPS without public DNS names or Let's
Encrypt. The gate keeps a private certificate authority. You trust its root certificate once on each device,
and from then on two things work without warnings:

- **reverse proxy hosts with internal certificates** ([[Reverse Proxy]]): names inside your network such as
  `nas.home.lan`, served by the gate's proxy
- **an ACME directory on the LAN**: the NAS, Proxmox, Home Assistant, a Linux server with certbot or any
  other ACME client gets its own certificates from the gate, exactly like from Let's Encrypt

The CA runs in the reverse proxy's service: a root (valid for 10 years) and an intermediate (renewed
automatically) kept in its state directory. The key never leaves the gate.

## Turn it on

The **Certificate authority** card on **Network → Reverse Proxy** (or `internal_ca` in the config):

| Setting | What it does |
|---|---|
| enabled | create the CA (on the first commit with it on) |
| name | the name in its certificates, default *Goblin Gate Local CA*: what your devices show when you trust it |
| acme | serve the ACME directory on the gate's LAN-side addresses, port 9443 |
| acme allow | who may use the directory: address objects or networks. Empty: every LAN-side network |
| lifetime hours | how long the proxy's internal certificates are valid, default 48 (they renew by themselves) |

After the commit the card shows the root's fingerprint and the ACME directory URLs, such as
`https://gate.home.lan:9443/acme/local/directory` and `https://192.168.1.1:9443/acme/local/directory`.
The directory is reachable from the LAN side only, never from the internet.

## Get the root certificate

Download it from the **Certificate authority** card, or straight from the gate without logging in (the root
certificate is public, only its key is secret):

- `https://<gate>/api/v1/internal-ca/root.crt` — PEM, for Linux, Firefox and most servers
- `https://<gate>/api/v1/internal-ca/root.crt?format=der` — DER (`.crt`), which phones and Windows offer to
  install when you open it

The first time, your browser warns about the gate's own certificate; that is expected. Before you trust the
download, compare its **SHA-256 fingerprint** with the one on the Certificate authority card.

## Trust it on your devices

**Windows**: double-click the `.crt` → **Install Certificate** → **Local Machine** → **Place all certificates
in the following store** → **Trusted Root Certification Authorities**. Edge and Chrome use it at once.

**macOS**: double-click the file; Keychain Access opens. Add it to the **System** keychain, open it, and under
**Trust** set **When using this certificate** to **Always Trust**.

**iPhone and iPad**: open the DER link in Safari, allow the download, then **Settings → Profile Downloaded →
Install**. Then **Settings → General → About → Certificate Trust Settings** and switch on full trust for the
root. Without that second step, iOS installs the certificate but does not trust it.

**Android**: **Settings → Security → More security settings → Encryption & credentials → Install a
certificate → CA certificate** and pick the downloaded file (menu names vary by maker). Apps trust user CAs
only if they choose to; Chrome does, many other apps do not.

**Linux (Debian, Ubuntu)**:

```sh
sudo curl -k -o /usr/local/share/ca-certificates/goblin-gate.crt https://192.168.1.1/api/v1/internal-ca/root.crt
sudo update-ca-certificates
```

Fedora and others: put the file in `/etc/pki/ca-trust/source/anchors/` and run `sudo update-ca-trust`.

**Firefox** keeps its own list. Either **Settings → Privacy & Security → Certificates → View Certificates →
Authorities → Import** (tick *Trust this CA to identify websites*), or let it use the system's: set
`security.enterprise_roots.enabled` to `true` in `about:config` (the default on Windows and macOS).

## ACME clients

Point the client at the directory URL and use the HTTP challenge: the gate checks that the name resolves to
the machine asking (with the gate's DNS: a DHCP hostname or a local record) and that the machine answers on
port 80. Trust the root on that machine first, since the directory itself is served with a certificate from
the CA. Certificates from the ACME directory are valid for 12 hours, so the client must renew often. Most
clients renew a certificate that is close to expiry on every run, so run them several times a day.

- **certbot**: `certbot certonly --standalone --server https://gate.home.lan:9443/acme/local/directory -d nas.home.lan`
  (its timer runs twice a day; add `--deploy-hook` to reload your web server)
- **acme.sh**: `acme.sh --issue --standalone --server https://gate.home.lan:9443/acme/local/directory -d nas.home.lan`
- **Proxmox VE**: **Datacenter → ACME → Accounts → Add**, directory URL as above (custom), then the node's
  **Certificates → ACME** with the HTTP plugin
- **Caddy, Traefik, Nginx Proxy Manager** on another machine: set the ACME CA URL to the directory

## Reverse proxy hosts with internal certificates

Pick **issuer: internal** for a reverse proxy host and give it a name inside your network, such as
`nas.home.lan`. The gate's DNS answers that name with `198.18.0.53`, an address the gate keeps for this and
sends to the proxy (the gate's own LAN address keeps port 443 for its admin page). It works for every client
that uses the gate for DNS. Such a host is open to the LAN-side networks only, unless you set its own
**allow** list.

## Safety

- A device that trusts the root trusts every certificate the gate issues, **for any name**. Trust it only on
  your own devices, and keep the gate's admin access tight: whoever controls the gate could make
  certificates for any website.
- The ACME directory gives a certificate to a machine that can answer for the name, the way Let's Encrypt
  does. Names in your local domain come from DHCP hostnames, so on a network with guests, limit **acme
  allow** to your server network.
- Turning the CA off stops issuing. The root stays in the gate's state, so turning it on again keeps the
  same root. To start over with a new root, remove `/var/lib/goblin-caddy/pki` as root while the CA is off,
  then turn it on again and trust the new root everywhere.
