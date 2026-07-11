# macOS code signing & notarization

Homebrew **casks** apply macOS's quarantine attribute to what they install
(formulas don't), so unsigned binaries trigger Gatekeeper's "developer
cannot be verified" block for every `brew install xPapay/tap/localhoist`
user. The release pipeline signs (Developer ID) and notarizes the darwin
binaries automatically — via goreleaser's built-in `notarize` support
(quill), directly on the Linux runner.

The pipeline is **dormant** until the secrets below exist (the
`isEnvSet "MACOS_SIGN_P12"` gate in `.goreleaser.yaml`); releases work
unsigned without them. Once the secrets are set, the next tag ships
signed + notarized darwin binaries — nothing else changes, and the cask
checksums stay correct because signing happens before checksumming.

## One-time setup

### 1. Apple Developer Program

Enroll at https://developer.apple.com/programs/enroll/ ($99/year, personal
account is fine).

### 2. Developer ID Application certificate

1. Keychain Access → Certificate Assistant → *Request a Certificate From a
   Certificate Authority…* → save the CSR to disk.
2. https://developer.apple.com/account/resources/certificates/add →
   choose **Developer ID Application** → upload the CSR → download the
   certificate and double-click it into your keychain.
3. Keychain Access → My Certificates → right-click
   *Developer ID Application: Lukas Papay (TEAMID)* → Export → `.p12`,
   choose a strong password.

### 3. App Store Connect API key (for the notary service)

1. https://appstoreconnect.apple.com → Users and Access → Integrations →
   App Store Connect API → Team Keys → generate a key with the
   **Developer** role.
2. Note the **Issuer ID** and **Key ID**; download the `.p8` file (only
   downloadable once).

### 4. Set the repository secrets

```sh
base64 -i DeveloperID.p12 | gh secret set MACOS_SIGN_P12 --repo xPapay/localhoist
gh secret set MACOS_SIGN_PASSWORD --repo xPapay/localhoist       # the .p12 export password
gh secret set MACOS_NOTARY_ISSUER_ID --repo xPapay/localhoist    # UUID from App Store Connect
gh secret set MACOS_NOTARY_KEY_ID --repo xPapay/localhoist       # e.g. 2X9R4HXF34
gh secret set MACOS_NOTARY_KEY --repo xPapay/localhoist < AuthKey_XXXXXXXXXX.p8
```

All five must be set — with only some of them, signing starts but
notarization fails and the release aborts.

## Verifying a release

```sh
codesign -dv --verbose=2 /opt/homebrew/bin/localhoist   # Authority: Developer ID Application: …
spctl -a -vv -t install /opt/homebrew/bin/localhoist    # accepted, source=Notarized Developer ID
```

Bare Mach-O binaries can't have the notarization ticket stapled (only
apps/dmg/pkg can), so Gatekeeper does a one-time online check — that's
normal and works offline afterwards.
