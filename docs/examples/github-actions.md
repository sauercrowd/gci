# GitHub Actions Deploy

Deploy with GCI from a GitHub Actions workflow.

Store your SSH private key in your GitHub repository first:

1. Open your repository on GitHub.
2. Go to `Settings` -> `Secrets and variables` -> `Actions`.
3. Add a new repository secret named `GCI_SSH_PRIVATE_KEY`.
4. Paste the private key contents for the server user that should run deploys.

You will usually also want repository or environment variables for the SSH host and SSH user used by `gci server add`.

Example:

- `GCI_HOST`: `your-server.example.com`
- `GCI_USER`: `deploy`

This workflow writes the SSH key to disk, adds the server entry for the current runner, and then runs `gci deploy`.

`.github/workflows/deploy.yml`:

```yaml
name: Deploy

on:
  push:
    branches:
      - main

jobs:
  deploy:
    runs-on: ubuntu-latest

    steps:
      - uses: actions/checkout@v4

      - name: Install GCI
        run: go install https://github.com/sauercrowd/gci@latest

      - name: Add GCI to PATH
        run: echo "$HOME/go/bin" >> "$GITHUB_PATH"

      - name: Write SSH key
        env:
          GCI_SSH_PRIVATE_KEY: ${{ secrets.GCI_SSH_PRIVATE_KEY }}
        run: |
          mkdir -p ~/.ssh
          printf '%s\n' "$GCI_SSH_PRIVATE_KEY" > ~/.ssh/gci_deploy_key
          chmod 600 ~/.ssh/gci_deploy_key

      - name: Trust server host key
        env:
          GCI_HOST: ${{ vars.GCI_HOST }}
        run: |
          mkdir -p ~/.ssh
          ssh-keyscan -H "$GCI_HOST" >> ~/.ssh/known_hosts

      - name: Register deploy target
        env:
          GCI_HOST: ${{ vars.GCI_HOST }}
          GCI_USER: ${{ vars.GCI_USER }}
        run: |
          gci server add prod \
            --host "$GCI_HOST" \
            --user "$GCI_USER" \
            --private-key ~/.ssh/gci_deploy_key

      - name: Deploy
        run: gci deploy
```

Notes:

- `gci server add` should run inside the workflow because the server config is local to the runner.
- Passing `--user` explicitly is recommended because the GitHub runner username usually does not match the SSH user on your server.
- `ssh-keyscan` avoids interactive host verification prompts. Verify the host key out of band before trusting it in CI.
- If you deploy from protected environments, move the secret and variables to a GitHub Environment and require approvals there.
