#!/usr/bin/env node

import { createHash } from "node:crypto";
import { createRequire } from "node:module";
import {
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  writeFileSync,
} from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const scriptDirectory = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(scriptDirectory, "..");
const resultsRoot = resolve(
  repositoryRoot,
  process.argv[2] ?? "web/test-results",
);
const version = process.argv[3] ?? process.env.VERSION ?? "";

if (!/^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(version)) {
  console.error(
    "usage: node scripts/publish-doc-screenshots.mjs [test-results] vX.Y.Z",
  );
  process.exit(2);
}
if (!existsSync(resultsRoot)) {
  throw new Error(`Playwright output does not exist: ${resultsRoot}`);
}

const pages = [
  {
    id: "today",
    screen: "today",
    route: "/",
    routePattern: "/",
    access: "anonymous",
    title: "Today",
    alt: "추천 앱과 카테고리를 보여 주는 AppStore Today 전체 화면",
  },
  {
    id: "today-alias",
    screen: "today-alias",
    route: "/today",
    routePattern: "/today",
    access: "anonymous",
    title: "Today URL alias",
    alt: "새로고침 뒤에도 복원되는 Today 경로 전체 화면",
  },
  {
    id: "apps",
    screen: "apps",
    route: "/apps?q=agent&sort=trending",
    routePattern: "/apps",
    access: "anonymous",
    title: "Apps",
    alt: "검색어와 정렬 상태가 URL에 유지된 공개 앱 카탈로그 전체 화면",
  },
  {
    id: "app-detail",
    screen: "app-detail",
    route: "/apps/agent-hub",
    routePattern: "/apps/:slug",
    access: "anonymous",
    title: "App detail",
    alt: "Agent Hub 설명과 서비스 접속 URL을 보여 주는 앱 상세 전체 화면",
  },
  {
    id: "categories",
    screen: "categories",
    route: "/categories",
    routePattern: "/categories",
    access: "anonymous",
    title: "Categories",
    alt: "공개 앱 카테고리 목록 전체 화면",
  },
  {
    id: "category-detail",
    screen: "category-detail",
    route: "/categories/ai",
    routePattern: "/categories/:category",
    access: "anonymous",
    title: "AI category",
    alt: "AI 카테고리로 필터링한 앱 목록 전체 화면",
  },
  {
    id: "search-alias",
    screen: "search-alias",
    route: "/search?q=agent",
    routePattern: "/search",
    access: "anonymous",
    title: "Search compatibility route",
    alt: "검색 상태를 앱 목록으로 전달하는 호환 검색 경로 전체 화면",
  },
  {
    id: "mcp-apps",
    screen: "mcp-apps",
    route: "/apps?mcp=true",
    routePattern: "/apps",
    variant: "mcp",
    access: "anonymous",
    title: "MCP Apps",
    alt: "MCP 지원 앱만 표시한 공개 카탈로그 전체 화면",
  },
  {
    id: "favorites",
    screen: "favorites",
    route: "/favorites",
    routePattern: "/favorites",
    access: "anonymous",
    title: "Favorites",
    alt: "브라우저에 저장된 즐겨찾기 앱 전체 화면",
  },
  {
    id: "forbidden",
    screen: "forbidden",
    route: "/403",
    routePattern: "/403",
    access: "anonymous",
    title: "Forbidden state",
    alt: "권한 부족 안내와 복귀 동작을 제공하는 403 전체 화면",
  },
  {
    id: "not-found",
    screen: "not-found",
    route: "/missing-page",
    routePattern: "*",
    access: "anonymous",
    title: "Not found state",
    alt: "존재하지 않는 URL의 404 전체 화면",
  },
  {
    id: "login",
    screen: "login",
    route: "/login?returnTo=%2Fsubmit",
    routePattern: "/login",
    access: "anonymous",
    title: "SSO login",
    alt: "Keycloak SSO 버튼과 AppStore v2.0.0 버전이 표시된 로그인 전체 화면",
  },
  {
    id: "admin-bootstrap-alias",
    screen: "admin-bootstrap-alias",
    route: "/admin/bootstrap",
    routePattern: "/admin/bootstrap",
    access: "anonymous",
    title: "Admin bootstrap login redirect",
    alt: "최초 관리자 인증 설정으로 돌아가는 로그인 전체 화면",
  },
  {
    id: "submit",
    screen: "submit",
    route: "/submit",
    routePattern: "/submit",
    access: "authenticated",
    title: "Submit app",
    alt: "인증된 사용자의 앱 등록 양식 전체 화면",
  },
  {
    id: "my-home",
    screen: "my-home",
    route: "/my",
    routePattern: "/my",
    access: "authenticated",
    title: "Personal dashboard",
    alt: "내 앱과 Key 현황을 보여 주는 개인화 대시보드 전체 화면",
  },
  {
    id: "my-apps",
    screen: "my-apps",
    route: "/my/apps",
    routePattern: "/my/apps",
    access: "authenticated",
    title: "My apps",
    alt: "사용자가 등록한 앱 목록 전체 화면",
  },
  {
    id: "my-app-edit",
    screen: "my-app-edit",
    route: "/my/apps/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa/edit",
    routePattern: "/my/apps/:id/edit",
    access: "authenticated",
    title: "Edit my app",
    alt: "소유한 앱의 정보를 수정하는 양식 전체 화면",
  },
  {
    id: "my-keys",
    screen: "my-keys",
    route: "/my/keys",
    routePattern: "/my/keys",
    access: "authenticated",
    title: "Personal API and MCP keys",
    alt: "개인 API 및 MCP Key와 회전 정책을 관리하는 전체 화면",
  },
  {
    id: "my-profile",
    screen: "my-profile",
    route: "/my/profile",
    routePattern: "/my/profile",
    access: "authenticated",
    title: "Profile",
    alt: "사용자 정보와 AppStore 버전을 확인하는 프로필 전체 화면",
  },
  {
    id: "my-activity",
    screen: "my-activity",
    route: "/my/activity",
    routePattern: "/my/activity",
    access: "authenticated",
    title: "My activity",
    alt: "사용자의 앱과 Key 활동 내역 전체 화면",
  },
  {
    id: "my-settings",
    screen: "my-settings",
    route: "/my/settings",
    routePattern: "/my/settings",
    access: "authenticated",
    title: "Personal settings",
    alt: "테마와 접근성 기본값을 관리하는 개인 설정 전체 화면",
  },
  {
    id: "my-favorites-alias",
    screen: "my-favorites-alias",
    route: "/my/favorites",
    routePattern: "/my/favorites",
    access: "authenticated",
    title: "Personal favorites redirect",
    alt: "개인 메뉴에서 공개 즐겨찾기로 복원된 전체 화면",
  },
  {
    id: "review",
    screen: "review",
    route: "/review",
    routePattern: "/review",
    access: "reviewer",
    title: "Review queue",
    alt: "팀장 및 Reviewer의 앱 검토 대기 목록 전체 화면",
  },
  {
    id: "review-detail",
    screen: "review-detail",
    route: "/review/dddddddd-dddd-4ddd-8ddd-dddddddddddd",
    routePattern: "/review/:id",
    access: "reviewer",
    title: "Review detail",
    alt: "앱 승인 또는 반려를 결정하는 검토 상세 전체 화면",
  },
  {
    id: "admin-dashboard",
    screen: "admin-dashboard",
    route: "/admin",
    routePattern: "/admin",
    access: "admin",
    title: "Admin dashboard",
    alt: "앱과 사용자 운영 상태를 요약한 서비스 관리자 대시보드 전체 화면",
  },
  {
    id: "admin-apps",
    screen: "admin-apps",
    route: "/admin/apps",
    routePattern: "/admin/apps",
    access: "admin",
    title: "Admin apps",
    alt: "모든 등록 앱을 관리하는 관리자 목록 전체 화면",
  },
  {
    id: "admin-app-detail",
    screen: "admin-app-detail",
    route: "/admin/apps/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    routePattern: "/admin/apps/:id",
    access: "admin",
    title: "Admin app detail",
    alt: "앱 상세 정보와 게시 상태를 수정하고 삭제하는 관리자 상세 전체 화면",
  },
  {
    id: "admin-categories",
    screen: "admin-categories",
    route: "/admin/categories",
    routePattern: "/admin/categories",
    access: "admin",
    title: "Admin categories",
    alt: "앱 카테고리를 관리하는 관리자 전체 화면",
  },
  {
    id: "admin-users",
    screen: "admin-users",
    route: "/admin/users",
    routePattern: "/admin/users",
    access: "admin",
    title: "Admin users",
    alt: "테마에 맞춘 스크롤 목록으로 사용자를 관리하는 전체 화면",
  },
  {
    id: "admin-roles",
    screen: "admin-roles",
    route: "/admin/roles",
    routePattern: "/admin/roles",
    access: "admin",
    title: "Roles and permissions",
    alt: "변경 가능한 역할과 권한 체계를 관리하는 전체 화면",
  },
  {
    id: "admin-reviews",
    screen: "admin-reviews",
    route: "/admin/reviews",
    routePattern: "/admin/reviews",
    access: "admin",
    title: "Admin reviews",
    alt: "서비스 전체 앱 검토 현황을 관리하는 전체 화면",
  },
  {
    id: "admin-workflow",
    screen: "admin-workflow",
    route: "/admin/workflow",
    routePattern: "/admin/workflow",
    access: "admin",
    title: "Approval workflow",
    alt: "선택형 팀장 검토와 승인 정책을 구성하는 전체 화면",
  },
  {
    id: "admin-ai",
    screen: "admin-ai",
    route: "/admin/ai",
    routePattern: "/admin/ai",
    access: "admin",
    title: "AI providers",
    alt: "AI 공급자와 최대 262144 토큰 및 Streaming을 설정하는 전체 화면",
  },
  {
    id: "admin-api",
    screen: "admin-api",
    route: "/admin/api",
    routePattern: "/admin/api",
    access: "admin",
    title: "REST API",
    alt: "REST API 공개 범위와 호출 정책을 구성하는 전체 화면",
  },
  {
    id: "admin-mcp",
    screen: "admin-mcp",
    route: "/admin/mcp",
    routePattern: "/admin/mcp",
    access: "admin",
    title: "MCP server",
    alt: "MCP 접근 방식과 도구 권한을 구성하는 전체 화면",
  },
  {
    id: "admin-api-keys",
    screen: "admin-keys",
    route: "/admin/api-keys",
    routePattern: "/admin/api-keys",
    access: "admin",
    title: "Admin API keys",
    alt: "서비스 전체 API Key 상태와 폐기를 관리하는 전체 화면",
  },
  {
    id: "admin-authentication",
    screen: "admin-authentication",
    route: "/admin/authentication",
    routePattern: "/admin/authentication",
    access: "admin",
    title: "Keycloak OIDC",
    alt: "Keycloak OIDC discovery와 역할 매핑을 설정하는 전체 화면",
  },
  {
    id: "admin-security",
    screen: "admin-security",
    route: "/admin/security",
    routePattern: "/admin/security",
    access: "admin",
    title: "Security and key policy",
    alt: "암호화와 개인 Key 회전 정책을 관리하는 보안 전체 화면",
  },
  {
    id: "admin-audit",
    screen: "admin-audit",
    route: "/admin/audit",
    routePattern: "/admin/audit",
    access: "admin",
    title: "Audit logs",
    alt: "삭제할 수 없는 관리자 감사 로그 전체 화면",
  },
  {
    id: "admin-settings",
    screen: "admin-settings",
    route: "/admin/settings",
    routePattern: "/admin/settings",
    access: "admin",
    title: "System settings",
    alt: "서비스 접속 URL과 공개 모드를 관리하는 시스템 설정 전체 화면",
  },
  {
    id: "today-light",
    screen: "today-light",
    route: "/",
    routePattern: "/",
    variant: "light",
    access: "anonymous",
    title: "Today light theme",
    alt: "라이트 테마가 적용된 AppStore Today 전체 화면",
  },
];

const viewports = {
  desktop: {
    cssWidth: 1440,
    cssHeight: 1000,
    deviceScaleFactor: 1,
  },
  mobile: {
    cssWidth: 412,
    cssHeight: 839,
    deviceScaleFactor: 2.625,
  },
};

function walk(directory) {
  const files = [];
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const absolute = join(directory, entry.name);
    if (entry.isDirectory()) files.push(...walk(absolute));
    else files.push(absolute);
  }
  return files;
}

const allResults = walk(resultsRoot);
function sourceFor(screen, viewport) {
  const suffix = `/screens/${screen}.png`;
  const matches = allResults.filter((file) => {
    const portable = file.replaceAll("\\", "/");
    return portable.endsWith(suffix) && portable.includes(`-${viewport}/`);
  });
  if (matches.length !== 1) {
    throw new Error(
      `expected one ${viewport} source for ${screen}, found ${matches.length}: ${matches.map((item) => relative(repositoryRoot, item)).join(", ")}`,
    );
  }
  return matches[0];
}

function pngDimensions(buffer) {
  if (buffer.toString("ascii", 1, 4) !== "PNG") {
    throw new Error("capture is not a PNG");
  }
  return { width: buffer.readUInt32BE(16), height: buffer.readUInt32BE(20) };
}

const require = createRequire(import.meta.url);
const { chromium } = require(
  join(repositoryRoot, "web/node_modules/playwright"),
);
const browser = await chromium.launch({ headless: true });
const converter = await browser.newPage();
const outputDirectory = join(
  repositoryRoot,
  "docs/assets/screenshots/captures",
);
mkdirSync(outputDirectory, { recursive: true });

const captures = [];
try {
  for (const viewport of Object.keys(viewports)) {
    for (const page of pages) {
      const source = sourceFor(page.screen, viewport);
      const png = readFileSync(source);
      const dimensions = pngDimensions(png);
      const dataUrl = await converter.evaluate(async (base64) => {
        const image = new Image();
        image.src = `data:image/png;base64,${base64}`;
        await image.decode();
        const canvas = document.createElement("canvas");
        canvas.width = image.naturalWidth;
        canvas.height = image.naturalHeight;
        const context = canvas.getContext("2d", { alpha: false });
        context.drawImage(image, 0, 0);
        return canvas.toDataURL("image/webp", 0.82);
      }, png.toString("base64"));
      if (!dataUrl.startsWith("data:image/webp;base64,")) {
        throw new Error(`Chromium did not encode WebP for ${page.screen}`);
      }
      const webp = Buffer.from(dataUrl.split(",", 2)[1], "base64");
      const output = `assets/screenshots/captures/${page.id}-${viewport}.webp`;
      writeFileSync(join(repositoryRoot, "docs", output), webp);
      captures.push({
        id: `${page.id}-${viewport}`,
        route: page.route,
        routePattern: page.routePattern,
        ...(page.variant ? { variant: page.variant } : {}),
        access: page.access,
        viewport,
        output,
        title: page.title,
        alt: page.alt,
        status: "captured",
        version,
        sha256: createHash("sha256").update(webp).digest("hex"),
        width: dimensions.width,
        height: dimensions.height,
        fullPage: true,
      });
    }
  }
} finally {
  await browser.close();
}

const manifest = {
  schemaVersion: 2,
  generatedForVersion: version,
  generatedAt: "2026-09-01T08:00:00Z",
  fixture: "web/e2e/mock-api.ts",
  captureSpec: "web/e2e/visual.spec.ts",
  capturePolicy: {
    fullPage: true,
    data: "deterministic, synthetic, and secret-free",
    format: "webp",
    quality: 82,
    viewports,
  },
  captures,
};
writeFileSync(
  join(repositoryRoot, "docs/assets/screenshots/manifest.json"),
  `${JSON.stringify(manifest, null, 2)}\n`,
);

console.log(
  `Published ${captures.length} full-page WebP captures for ${version}`,
);
