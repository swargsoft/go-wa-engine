# Release Process

Two repos, one coordinated release. Engine first, then web app.

## Prerequisites

1. Cloudflare Worker deployed (see `worker/README.md`)
2. `GITHUB_PAT` + `AUTH_TOKEN` set as worker secrets
3. `ENGINE_AUTH_KEY` set as GitHub Actions secret in `swargsoft/msgly`

## 1. Release walite (WAHA engine)

```bash
cd waha
git tag v1.2.0
git push origin v1.2.0
```

CI (`build-release.yml`) will:
- Build the NestJS app on all 5 platforms (darwin-arm64, darwin-amd64, linux-amd64, linux-arm64, windows-amd64)
- Package each with bundled `node_modules/` into platform tarballs (`.tar.gz` / `.zip`)
- Generate per-file SHA256 checksums
- Generate `SHA256SUMS` (aggregated) and `latest.json` (machine-readable manifest)
- Create a GitHub Release with all artifacts

**Verify:** `gh release view v1.2.0 --repo swargsoft/walite`

## 2. Release msgly (web app)

```bash
cd msgly
git tag v2.1.0
git push origin v2.1.0
```

CI (`release.yml`) will:
- Fetch latest walite manifest via the Cloudflare Worker proxy
- Embed engine version + auth key into the build
- Build the PWA
- Create a GitHub Release with the `dist/` folder

## 3. Verify

- Onboarding page shows engine health status (green dot = running)
- Download commands point to proxy URL (not direct GitHub)
- Unauthenticated requests to proxy get 401

## Manual download (for devs with GitHub access)

```bash
gh release download v1.2.0 --repo swargsoft/walite -p "walite-*-darwin-arm64.tar.gz"
tar xzf walite-*.tar.gz
cd walite-*/
./bin/walite start
```
