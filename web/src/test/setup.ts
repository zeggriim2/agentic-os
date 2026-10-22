import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";
import { setCsrfToken, onUnauthorized } from "@/api/client";

afterEach(() => {
  cleanup();
  setCsrfToken(null);
  onUnauthorized(null);
});
