// The documentation site of kickd, built with Starlight. .github/workflows/docs.yml
// publishes it to GitHub Pages at https://etak64n.github.io/kickd/.
import starlight from "@astrojs/starlight";
import { defineConfig } from "astro/config";
import starlightLinksValidator from "starlight-links-validator";

export default defineConfig({
  site: "https://etak64n.github.io",
  // GitHub Pages serves the site under the name of the repository.
  base: "/kickd",
  integrations: [
    starlight({
      title: "kickd",
      description: "Runs commands when something happens, on macOS, Linux and Windows.",
      // The logo stands for the title, in the copy that the color scheme of the page shows well.
      logo: {
        light: "./src/assets/logo-light.svg",
        dark: "./src/assets/logo-dark.svg",
        alt: "kickd",
        replacesTitle: true,
      },
      // The icon of the browser tab: favicon.svg turns its square light on dark tabs, and
      // favicon.png is for browsers without SVG icons.
      favicon: "/favicon.svg",
      head: [{ tag: "link", attrs: { rel: "icon", type: "image/png", sizes: "64x64", href: "/kickd/favicon.png" } }],
      social: [{ icon: "github", label: "GitHub", href: "https://github.com/etak64n/kickd" }],
      editLink: { baseUrl: "https://github.com/etak64n/kickd/edit/main/docs/" },
      customCss: ["./src/styles/custom.css"],
      // A link to a missing page, or to a heading that the page does not have, fails the build.
      plugins: [starlightLinksValidator()],
      // Getting started, guides to the features, settings, references, internals,
      // and the project itself.
      sidebar: [
        {
          label: "Getting started",
          items: [
            "getting-started/quickstart",
            "getting-started/install-macos",
            "getting-started/install-linux",
            "getting-started/install-windows",
          ],
        },
        {
          label: "Guides",
          items: [
            "guides/how-kickd-works",
            "guides/configuration",
            "guides/runs",
            "guides/commands",
            "guides/users",
            "guides/webhooks",
            { label: "Examples", link: "https://github.com/etak64n/kickd/tree/main/examples" },
          ],
        },
        {
          label: "Settings",
          items: [
            "settings/config-keys",
            "settings/log",
            "settings/webhook",
            "settings/database",
            "settings/events",
            "settings/params",
            "settings/triggers",
          ],
        },
        { label: "Reference", items: ["reference/cli", "reference/payload", "reference/logging"] },
        {
          label: "Internals",
          items: [
            "internals/architecture",
            "internals/database",
            "internals/idempotency",
            "internals/execution",
            "internals/platforms",
          ],
        },
        { label: "Project", items: ["project/limitations", "project/build", "project/development"] },
      ],
    }),
  ],
});
