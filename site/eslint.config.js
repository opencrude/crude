import js from "@eslint/js";
import { defineConfig, globalIgnores } from "eslint/config";
import globals from "globals";
import tseslint from "typescript-eslint";
import tsParser from "@typescript-eslint/parser";
import reactRefresh from "eslint-plugin-react-refresh";
import reactHooks from "eslint-plugin-react-hooks";
import { plugin as shadcn } from "@shadcn/lint";
import designSystem from "./design-system.lint.json" with { type: "json" };

export default defineConfig([
  globalIgnores(["dist", "node_modules"]),
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ["**/*.{ts,tsx}"],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
      parser: tsParser,
      parserOptions: { ecmaFeatures: { jsx: true } },
    },
    plugins: {
      shadcn,
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
    },
    settings: {
      shadcn: {
        ui: "@/components/ui",
        note: "Use the shared shadcn/ui components and theme tokens. Do not restyle a component with className; use an existing variant, or add a variant to the component.",
      },
    },
    rules: {
      ...designSystem.rules,
      "react-hooks/rules-of-hooks": "error",
      "react-hooks/exhaustive-deps": "warn",
      "react-refresh/only-export-components": "off",
    },
  },
  ...designSystem.overrides,
]);
