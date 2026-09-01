import { describe, expect, it } from "vitest";
import { clampToken, hasAnyRole, normalizeRoles, safeJsonParse } from "./utils";

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
});
