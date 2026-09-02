import { describe, expect, it } from "vitest";
import {
  clampToken,
  hasAnyRole,
  normalizeRoles,
  safeJsonParse,
  safeReturnTo,
} from "./utils";

describe("frontend policy utilities", () => {
  it("accepts the canonical backend role names and normalizes Keycloak aliases", () => {
    expect(normalizeRoles(["appstore-team-leader", "SUPER-ADMIN"])).toEqual([
      "team_leader",
      "super_admin",
    ]);
    expect(hasAnyRole(["appstore-super-admin"], ["super_admin"])).toBe(true);
  });

  it("keeps AI token values inside the supported 256K range", () => {
    expect(clampToken(-1)).toBe(0);
    expect(clampToken(262_144)).toBe(262_144);
    expect(clampToken(999_999)).toBe(262_144);
  });

  it("falls back safely when persisted JSON is invalid", () => {
    expect(safeJsonParse("not-json", ["fallback"])).toEqual(["fallback"]);
  });

  it("keeps the post-login redirect on this origin", () => {
    for (const unsafe of [
      null,
      "",
      "https://evil.test",
      "//evil.test",
      "/\\evil.test",
      "\\\\evil.test",
      "/\t/evil.test",
      "/\n/evil.test",
      "//evil.test",
    ]) {
      expect(safeReturnTo(unsafe)).toBe("/");
    }
    expect(safeReturnTo("/submit?draft=1")).toBe("/submit?draft=1");
    expect(safeReturnTo("/search?q=%2F%5C")).toBe("/search?q=%2F%5C");
  });
});
