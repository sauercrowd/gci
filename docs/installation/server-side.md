# Server Side Installation

## Requirements

- Linux server with SSH access
- Docker Engine installed

Docker installation guide:

- <https://docs.docker.com/engine/install/>

## Initialize Docker Swarm

Run once on your target server:

```bash
docker swarm init
```

## Optional: Run a Local Registry

Recommended so your build machine can push images to the server safely over SSH forwards:

```bash
docker run -d \
  --name local-registry \
  --restart always \
  -p 127.0.0.1:41114:5000 \
  registry:3
```

Next step: [Client Side Installation](/installation/client-side).
