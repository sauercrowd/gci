import { defineConfig } from "vitepress";

export default defineConfig({
  title: "GCI",
  description: "Fly.io style deployments for any SSH server",
  cleanUrls: true,
  themeConfig: {
    logo: "/logo.png",
    nav: [
      { text: "Installation", link: "/installation/" },
      { text: "Configuration", link: "/configuration/" },
      { text: "Reference", link: "/reference/commands" },
      { text: "GitHub", link: "https://github.com/sauercrowd/gci" }
    ],
    sidebar: [
      {
        text: "Installation",
        items: [
          { text: "Overview", link: "/installation/" },
          { text: "Server Side", link: "/installation/server-side" },
          { text: "Client Side", link: "/installation/client-side" },
          { text: "Cloudflare Deployment", link: "/installation/cloudflare-deployment" }
        ]
      },
      {
        text: "Configuration",
        items: [
          { text: "Overview", link: "/configuration/" },
          { text: "gci.toml Overview", link: "/configuration/gci-toml" },
          { text: "Build", link: "/configuration/build" },
          { text: "Sync", link: "/configuration/sync" },
          { text: "Docker Swarm", link: "/configuration/docker-swarm" }
        ]
      },
      {
        text: "Example Configurations",
        items: [
          { text: "Overview", link: "/examples/" },
          { text: "Local Build", link: "/examples/local-build" },
          { text: "Remote Build", link: "/examples/remote-build" },
          { text: "Postgres", link: "/examples/postgres" },
          { text: "Using Migrations", link: "/examples/using-migrations" },
          { text: "No Build Steps", link: "/examples/no-build-steps" },
          { text: "Controlling Dependencies", link: "/examples/controlling-dependencies" },
          { text: "Cloudflare Tunnel Webapp", link: "/examples/cloudflare-tunnel-webapp" },
          { text: "React + Express + Nginx", link: "/examples/react-express-nginx" },
          { text: "Caddy Reverse Proxy", link: "/examples/caddy" },
          { text: "Using Template Variables", link: "/examples/template-variables" },
          { text: "Rolling Releases", link: "/examples/rolling-releases" }
        ]
      },
      {
        text: "Reference",
        items: [{ text: "Commands", link: "/reference/commands" }]
      }
    ],
    socialLinks: [
      { icon: "github", link: "https://github.com/sauercrowd/gci" }
    ],
    search: {
      provider: "local"
    }
  }
});
