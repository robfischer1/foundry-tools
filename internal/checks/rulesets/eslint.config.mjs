// Root ESLint flat config — the strict Forge baseline (typescript-eslint
// strict + stylistic, type-checked). Apps inherit this by walking up from their
// own directory; framework-specific rules layer on top per app.
//
// This file is the FLEET'S: ts:bun-gate writes it over every repo's own copy
// (foundry-tools atoms_ts.go), so anything a repo wants enforced at the door
// lives here or does not run there.
import { existsSync } from "node:fs";
import js from "@eslint/js";
import tseslint from "typescript-eslint";

// THE GIJMO DESIGN-SYSTEM RULES, FOR REPOS THAT CONSUME @gijmo/ui (today: theia,
// and gijmo-ui itself, which depends on its own workspace package). Presence is
// read off the install the root config resolves against — a repo that declares
// the dependency has it at <root>/node_modules/@gijmo/ui. The probe is only the
// guard; the import after it is NOT wrapped, so a consumer whose plugin is
// broken (dist not built, bad export) fails the gate loudly instead of silently
// dropping 21 rules. A repo without @gijmo/ui never reaches the import.
const gijmoInstalled = existsSync(
  new URL("./node_modules/@gijmo/ui/package.json", import.meta.url),
);
const gijmo = gijmoInstalled
  ? (await import("@gijmo/ui/eslint")).default
  : undefined;

const gijmoConfigs = gijmo
  ? [
      // Static assets served verbatim, never source (gijmo-ui's gallery mirrors
      // the kit's specimen cards plus a vendored React/Babel runtime; linting
      // 4MB of third-party bundles fails on parse). Three spellings on purpose:
      // a global `ignores` resolves against the config's basePath, and a
      // package's own `eslint .` runs with the package as cwd.
      { ignores: ["**/public/**", "public/**"] },
      ...gijmo.configs.recommended,
      // Tests ASSERT ON CSS TEXT AND BUILD COMPARISON ELEMENTS; they do not
      // author product UI. A test that pins `gap: 10px` in a stylesheet it
      // reads back, or a `<Badge solid tone>` that expectGijmo compares
      // against, is not off-scale styling or colour-only status — the four
      // value-literal rules read it as if it were (theia#18094: 13 errors, all
      // under __tests__/).
      {
        files: ["**/__tests__/**/*.{ts,tsx}"],
        rules: {
          "gijmo/serif-face-scope": "off",
          "gijmo/font-size-token": "off",
          "gijmo/spacing-token": "off",
          "gijmo/status-not-color-alone": "off",
        },
      },
      // The plugin's own RuleTester fixtures deliberately contain violating
      // code — that is the INPUT a lint rule is tested against (58 errors, all
      // fixtures doing their job). Build scripts are not product UI either:
      // gijmo-ui's contrast.ts emits a specimen card reproducing the kit's
      // pre-ruling CSS, a design call rather than a lint fix.
      {
        files: ["**/__tests__/eslint/**", "**/scripts/**"],
        rules: Object.fromEntries(
          Object.keys(gijmo.rules).map((r) => [`gijmo/${r}`, "off"]),
        ),
      },
    ]
  : [];

export default tseslint.config(
  {
    ignores: [
      "**/.next/**",
      "**/dist/**",
      "**/node_modules/**",
      "**/*.config.*",
      "**/next-env.d.ts",
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.strictTypeChecked,
  ...tseslint.configs.stylisticTypeChecked,
  ...gijmoConfigs,
  {
    languageOptions: {
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
  },
  // Production source never reaches into __tests__/. The image build's context
  // drops **/__tests__ (.dockerignore), so an import that resolves on a
  // workstation and in the gate dies UNRESOLVED_IMPORT in the builder stage —
  // measured 2026-09-08, theia 5603331. A fixture the runtime needs lives
  // beside the code that needs it. Fleet-wide: every star's image build has
  // the same context rule, and no repo under Outputs/ violates it today.
  {
    files: ["apps/*/src/**/*.{ts,tsx}", "packages/*/src/**/*.{ts,tsx}"],
    rules: {
      "no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              regex: "(^|/)__tests__(/|$)",
              message:
                "src/ must not import from __tests__/ — .dockerignore drops it from the image build; keep a fixture the runtime needs beside the code.",
            },
          ],
        },
      ],
    },
  },
);
