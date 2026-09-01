import clsx, { type ClassValue } from "clsx";

export function cn(...values: ClassValue[]): string {
  return clsx(values);
}

export function safeJsonParse<T>(value: string | null, fallback: T): T {
  if (!value) return fallback;
  try {
    return JSON.parse(value) as T;
  } catch {
    return fallback;
  }
}

export function formatDate(value?: string): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat("ko-KR", { dateStyle: "medium" }).format(date);
}

export function formatDateTime(value?: string): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat("ko-KR", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}

export function initials(value?: string): string {
  const normalized = value?.trim();
  if (!normalized) return "AP";
  return [...normalized].slice(0, 2).join("").toUpperCase();
}

export function normalizeRoles(roles?: string[]): string[] {
  return (roles ?? []).map((role) =>
    role
      .toLowerCase()
      .replace(/^appstore[-_]/, "")
      .replace(/-/g, "_"),
  );
}

export function hasAnyRole(
  roles: string[] | undefined,
  expected: string[],
): boolean {
  const normalized = normalizeRoles(roles);
  return expected.some((role) => normalized.includes(role.toLowerCase()));
}

export function appGlyph(name: string, icon?: string): string {
  if (icon && !icon.startsWith("http")) return icon;
  return initials(name);
}

export function clampToken(value: number): number {
  if (!Number.isFinite(value)) return 0;
  return Math.min(262_144, Math.max(0, Math.round(value)));
}
