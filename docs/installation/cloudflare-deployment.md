# Cloudflare Deployment Setup

This project uses VitePress and can deploy docs automatically to Cloudflare Pages via GitHub Actions.

## 1. Create a Cloudflare Pages Project

Create a Pages project in Cloudflare and connect it to this GitHub repository.

Use these build settings:

- Framework preset: `None`
- Build command: `pnpm docs:build`
- Build output directory: `docs/.vitepress/dist`

## 2. Add Repository Secrets

In GitHub repository settings, add:

- `CLOUDFLARE_API_TOKEN`
- `CLOUDFLARE_ACCOUNT_ID`

The API token needs permissions for Cloudflare Pages deployments.

## 3. Set Project Name in Workflow

In `.github/workflows/deploy-docs-cloudflare.yml`, update:

```yaml
env:
  CLOUDFLARE_PROJECT_NAME: gci-docs
```

Set it to your actual Cloudflare Pages project name.

## 4. Deploy

Push to `main` and the workflow will build and deploy the docs site.
