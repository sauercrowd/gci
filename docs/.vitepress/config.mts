import { defineConfig } from "vitepress";

export default defineConfig({
  title: "GCI",
  description: "Nix + Incus deployments for any SSH server",
  cleanUrls: true,
  themeConfig: {
    logo: "/logo.png",
    nav: [
      { text: "Installation", link: "/installation/" },
      { text: "Configuration", link: "/configuration" },
      { text: "Commands", link: "/reference/commands" },
      { text: "GitHub", link: "https://github.com/sauercrowd/gci" }
    ],
    sidebar: [
      { text: "Overview", link: "/" },
      { text: "Installation", link: "/installation/" },
      { text: "Configuration", link: "/configuration" },
      { text: "Commands", link: "/reference/commands" }
    ],
    socialLinks: [
      { icon: "github", link: "https://github.com/sauercrowd/gci" }
    ],
    search: {
      provider: "local"
    }
  }
});
