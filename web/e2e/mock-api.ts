import type { Page } from "@playwright/test";

const category = {
  id: "11111111-1111-4111-8111-111111111111",
  slug: "ai",
  name: "AI · Automation",
  description: "에이전트와 자동화 서비스",
  appCount: 3,
  active: true,
};

const apps = [
  {
    id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    slug: "agent-hub",
    name: "Agent Hub",
    summary: "팀의 AI Agent를 발견하고 실행하는 통합 허브",
    description:
      "검증된 AI Agent와 자동화 도구를 한곳에서 탐색하고 안전하게 실행합니다.",
    icon: "AH",
    serviceUrl: "https://agent.internal.example",
    category,
    categoryId: category.id,
    tags: ["AI", "Agent", "MCP"],
    language: "Go",
    framework: "React",
    supportsMcp: true,
    supportsApi: true,
    ownerName: "김개발",
    team: "AI Platform",
    version: "2.4.0",
    visibility: "public",
    status: "published",
    featured: true,
    trendingScore: 98,
    updatedAt: "2026-09-01T08:00:00Z",
  },
  {
    id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
    slug: "flow-studio",
    name: "Flow Studio",
    summary: "반복 업무를 연결하는 시각적 자동화 빌더",
    description: "조직의 API와 이벤트를 연결해 업무 흐름을 구성합니다.",
    icon: "FS",
    serviceUrl: "https://flow.internal.example",
    category,
    categoryId: category.id,
    tags: ["Workflow", "API"],
    language: "TypeScript",
    framework: "React",
    supportsMcp: false,
    supportsApi: true,
    ownerName: "이자동",
    team: "Developer Experience",
    version: "1.8.1",
    visibility: "public",
    status: "published",
    featured: true,
    trendingScore: 72,
    updatedAt: "2026-08-30T06:00:00Z",
  },
  {
    id: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
    slug: "secure-vault",
    name: "Secure Vault",
    summary: "개발팀을 위한 키 회전과 접근 권한 도구",
    description: "개인별 키 수명 주기와 최소 권한 정책을 관리합니다.",
    icon: "SV",
    serviceUrl: "https://vault.internal.example",
    category,
    categoryId: category.id,
    tags: ["Security", "Keys"],
    language: "Go",
    framework: "React",
    supportsMcp: true,
    supportsApi: true,
    ownerName: "박보안",
    team: "Security",
    version: "3.1.0",
    visibility: "public",
    status: "published",
    featured: false,
    trendingScore: 55,
    updatedAt: "2026-08-28T03:00:00Z",
  },
];

const adminUser = {
  id: "99999999-9999-4999-8999-999999999999",
  username: "admin",
  email: "admin@example.internal",
  displayName: "AppStore 관리자",
  team: "Platform",
  active: true,
  roles: ["user", "contributor", "reviewer", "team_leader", "admin"],
  permissions: [
    "apps:submit",
    "apps:update",
    "reviews:read",
    "reviews:decide",
    "ai:use",
  ],
  updatedAt: "2026-09-01T08:00:00Z",
};

const review = {
  id: "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
  appId: apps[1]?.id,
  appName: "Flow Studio",
  appSlug: "flow-studio",
  submitterName: "이자동",
  team: "Developer Experience",
  level: 1,
  status: "pending",
  createdAt: "2026-09-01T07:00:00Z",
};

export interface MockApiOptions {
  authenticated?: boolean;
  roles?: string[];
}

export async function installMockApi(
  page: Page,
  options: MockApiOptions = {},
): Promise<void> {
  const authenticated = options.authenticated ?? false;
  const user = { ...adminUser, roles: options.roles ?? adminUser.roles };

  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const json = (body: unknown, status = 200) =>
      route.fulfill({
        status,
        contentType: "application/json; charset=utf-8",
        body: JSON.stringify(body),
      });

    if (path === "/api/version") {
      return json({
        version: "v2.0.0",
        commit: "4ab9102f",
        buildDate: "2026-09-01T08:00:00Z",
      });
    }
    if (path === "/api/v1/public/config") {
      return json({
        siteName: "AppStore",
        siteUrl: "https://appstore.example.internal",
        publicMode: true,
        oidcEnabled: true,
        oidcConfigured: true,
        workflowEnabled: true,
        anonymousMcp: false,
        theme: "system",
      });
    }
    if (path === "/api/v1/auth/session") {
      return json({
        authenticated,
        oidcConfigured: true,
        bootstrapRequired: false,
        bootstrapAvailable: true,
        csrfToken: authenticated ? "csrf-e2e" : undefined,
        user: authenticated ? user : undefined,
      });
    }
    if (path === "/api/v1/categories") return json([category]);
    if (path === "/api/v1/apps") {
      const q = (url.searchParams.get("q") ?? "").toLowerCase();
      const mcpOnly = url.searchParams.get("mcp") === "true";
      const featuredOnly = url.searchParams.get("featured") === "true";
      const filtered = apps.filter(
        (app) =>
          (!q ||
            `${app.name} ${app.summary} ${app.tags.join(" ")}`
              .toLowerCase()
              .includes(q)) &&
          (!mcpOnly || app.supportsMcp) &&
          (!featuredOnly || app.featured),
      );
      return json({
        items: filtered,
        total: filtered.length,
        limit: 24,
        offset: 0,
      });
    }
    if (path.startsWith("/api/v1/apps/") && request.method() === "GET") {
      const slug = decodeURIComponent(path.split("/").pop() ?? "");
      const app = apps.find((item) => item.slug === slug);
      return app
        ? json(app)
        : json(
            {
              error: {
                code: "APP_NOT_FOUND",
                message: "앱을 찾을 수 없습니다.",
              },
            },
            404,
          );
    }
    if (path === "/api/v1/me") return json(user);
    if (path === "/api/v1/me/apps") return json(apps.slice(0, 2));
    if (path === "/api/v1/me/keys") {
      return json([
        {
          id: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee",
          name: "개발 CLI",
          prefix: "aps_7sK8",
          permissions: ["apps:read", "apps:submit"],
          createdAt: "2026-08-01T08:00:00Z",
          expiresAt: "2026-11-01T08:00:00Z",
        },
      ]);
    }
    if (path === "/api/v1/me/activity") {
      return json([
        { action: "App Update", timestamp: "2026-09-01T08:00:00Z" },
      ]);
    }
    if (path === "/api/v1/me/settings")
      return json({
        theme: "system",
        language: "ko",
        reducedMotion: false,
        compactCards: false,
      });
    if (path === "/api/v1/me/key-permissions")
      return json({
        permissions: [
          {
            key: "apps:read",
            name: "앱 읽기",
            description: "앱 목록과 상세 조회",
            active: true,
          },
          {
            key: "apps:submit",
            name: "앱 등록",
            description: "새 앱 등록",
            active: true,
          },
          {
            key: "ai:use",
            name: "AI 사용",
            description: "AI Streaming API",
            active: true,
          },
          {
            key: "mcp:execute",
            name: "MCP 실행",
            description: "MCP tool 실행",
            active: true,
          },
        ],
        templates: [
          {
            id: "template-developer",
            name: "Developer",
            permissions: ["apps:read", "apps:submit"],
          },
        ],
        policy: {
          maxKeys: 5,
          defaultExpiryDays: 90,
          rotationGraceDays: 7,
          expireUnused: true,
          unusedExpiryDays: 30,
          forceRotation: false,
          forceRotationDays: 90,
        },
      });
    if (path === "/api/v1/reviews") return json([review]);
    if (path === `/api/v1/reviews/${review.id}`) return json(review);
    if (path === "/api/v1/ai/chat/stream") {
      return route.fulfill({
        status: 200,
        headers: { "content-type": "text/event-stream; charset=utf-8" },
        body: 'event: token\ndata: {"text":"스트리밍 응답"}\n\nevent: usage\ndata: {"totalTokens":12}\n\nevent: finish\ndata: {"finishReason":"stop"}\n\n',
      });
    }
    if (path === "/api/v1/admin/dashboard") {
      return json({
        appsTotal: 3,
        appsPublished: 3,
        reviewsPending: 1,
        usersActive: 150,
        oidcConfigured: true,
        workflowEnabled: true,
        aiStreaming: true,
      });
    }
    if (path === "/api/v1/admin/apps" && request.method() === "GET") {
      const q = (url.searchParams.get("q") ?? "").toLowerCase();
      const status = url.searchParams.get("status") ?? "";
      const mcpOnly = url.searchParams.get("mcp") === "true";
      const filtered = apps.filter(
        (app) =>
          (!q || `${app.name} ${app.summary}`.toLowerCase().includes(q)) &&
          (!status || app.status === status) &&
          (!mcpOnly || app.supportsMcp),
      );
      return json({
        items: filtered,
        total: filtered.length,
        limit: 200,
        offset: 0,
      });
    }
    if (path === "/api/v1/admin/apps" && request.method() === "POST") {
      return json(
        { ...apps[0], ...(request.postDataJSON() ?? {}), id: "new-app-id" },
        201,
      );
    }
    if (path.startsWith("/api/v1/admin/apps/")) {
      const id = decodeURIComponent(path.split("/").pop() ?? "");
      const app =
        apps.find((item) => item.id === id) ??
        (id === "new-app-id" ? apps[0] : undefined);
      if (!app)
        return json(
          {
            error: {
              code: "APP_NOT_FOUND",
              message: "앱을 찾을 수 없습니다.",
            },
          },
          404,
        );
      if (request.method() === "DELETE")
        return route.fulfill({ status: 204, body: "" });
      if (request.method() === "PUT")
        return json({ ...app, ...(request.postDataJSON() ?? {}) });
      return json(app);
    }
    if (
      path === "/api/v1/admin/authentication/test" &&
      request.method() === "POST"
    ) {
      const issuer = "https://sso.example.internal/realms/company";
      return json({
        ok: true,
        issuer,
        discoveryUrl: `${issuer}/.well-known/openid-configuration`,
        authorizationEndpoint: `${issuer}/protocol/openid-connect/auth`,
        tokenEndpoint: `${issuer}/protocol/openid-connect/token`,
        userInfoEndpoint: `${issuer}/protocol/openid-connect/userinfo`,
        endSessionEndpoint: `${issuer}/protocol/openid-connect/logout`,
        jwksUri: `${issuer}/protocol/openid-connect/certs`,
        scopesSupported: ["openid", "profile", "email"],
        pkceSupported: true,
        clientId: "appstore",
        clientSecretSet: true,
        redirectUrl:
          "https://appstore.example.internal/api/v1/auth/oidc/callback",
      });
    }
    if (path === "/api/v1/admin/categories") return json([category]);
    if (path === "/api/v1/admin/users") {
      const users = Array.from({ length: 150 }, (_, index) => ({
        ...adminUser,
        id: `user-${index}`,
        username: `developer${index + 1}`,
        displayName: `개발자 ${index + 1}`,
        email: `developer${index + 1}@example.internal`,
        roles:
          index % 5 === 0
            ? ["user", "contributor", "reviewer"]
            : ["user", "contributor"],
      }));
      return json({
        items: users,
        total: users.length,
        limit: 1000,
        offset: 0,
      });
    }
    if (path === "/api/v1/admin/roles") {
      return json({
        roles: [
          "user",
          "contributor",
          "reviewer",
          "team_leader",
          "admin",
          "super_admin",
        ],
        permissions: ["apps:read", "apps:submit", "ai:use", "mcp:execute"],
      });
    }
    if (path === "/api/v1/admin/workflow") {
      return json({
        enabled: true,
        levels: 1,
        reviewerRoles: ["reviewer"],
        teamLeaderRoles: ["team_leader"],
        autoPublish: true,
        rejectReasonRequired: true,
        reapprovalAfterEdit: true,
        preventSelfApproval: true,
      });
    }
    if (path === "/api/v1/admin/authentication") {
      return json({
        enabled: true,
        issuerUrl: "https://sso.example.internal/realms/company",
        clientId: "appstore",
        clientSecretSet: true,
        roleClaimPath: "realm_access.roles",
        groupClaimPath: "groups",
        roleMappings: {
          "appstore-admin": ["admin"],
          "appstore-reviewer": ["reviewer"],
        },
        groupMappings: {},
        scopes: ["openid", "profile", "email"],
      });
    }
    if (path === "/api/v1/admin/ai") {
      return json({
        id: "provider-1",
        name: "Internal vLLM",
        kind: "vllm",
        baseUrl: "https://ai.example.internal/v1",
        apiKeySet: true,
        defaultModel: "company-model",
        contextWindow: 262144,
        maxInputTokens: 240000,
        maxOutputTokens: 22144,
        temperature: 0.7,
        timeoutSeconds: 120,
        retries: 1,
        streaming: true,
        enabled: true,
      });
    }
    if (path === "/api/v1/admin/ai/models") {
      return json({
        items: [
          {
            id: "66666666-6666-4666-8666-666666666666",
            providerId: "provider-1",
            name: "company-model",
            contextWindow: 262144,
            maxInputTokens: 253952,
            maxOutputTokens: 8192,
            enabled: true,
          },
          {
            id: "77777777-7777-4777-8777-777777777777",
            providerId: "provider-1",
            name: "compact-model",
            contextWindow: 32768,
            maxInputTokens: 28672,
            maxOutputTokens: 4096,
            enabled: true,
          },
        ],
        total: 2,
        limit: 100,
        offset: 0,
      });
    }
    if (path === "/api/v1/admin/api")
      return json({ enabled: true, anonymous: true, rateLimitPerMinute: 120 });
    if (path === "/api/v1/admin/mcp")
      return json({
        enabled: true,
        anonymous: false,
        rateLimitPerMinute: 60,
        protocolVersion: "2026-07-28",
      });
    if (path === "/api/v1/admin/security")
      return json({
        maxKeys: 5,
        defaultExpiryDays: 90,
        rotationGraceDays: 7,
        expireUnused: true,
        unusedExpiryDays: 30,
        forceRotation: false,
        forceRotationDays: 90,
        permissions: [
          {
            key: "apps:read",
            name: "앱 읽기",
            description: "앱 목록과 상세 조회",
            active: true,
          },
          {
            key: "apps:submit",
            name: "앱 등록",
            description: "새 앱 등록",
            active: true,
          },
          {
            key: "ai:use",
            name: "AI 사용",
            description: "AI Streaming 호출",
            active: true,
          },
          {
            key: "mcp:execute",
            name: "MCP 실행",
            description: "MCP Tool 실행",
            active: true,
          },
        ],
        templates: [
          {
            id: "template-read",
            name: "Read Only",
            description: "조회 전용",
            permissions: ["apps:read"],
          },
          {
            id: "template-developer",
            name: "Developer",
            description: "앱 등록과 조회",
            permissions: ["apps:read", "apps:submit"],
          },
        ],
      });
    if (path === "/api/v1/admin/settings")
      return json({
        siteName: "AppStore",
        siteUrl: "https://appstore.example.internal",
        theme: "system",
        defaultLanguage: "ko",
        pageSize: 24,
        publicMode: true,
      });
    if (path === "/api/v1/admin/api-keys")
      return json([
        {
          id: "key-1",
          ownerName: "개발자 1",
          name: "CI",
          prefix: "aps_demo",
          permissions: ["apps:read"],
          createdAt: "2026-09-01T08:00:00Z",
        },
      ]);
    if (path === "/api/v1/admin/audit")
      return json({
        items: [
          {
            id: "audit-1",
            actor: "admin",
            action: "Setting Change",
            resource: "workflow",
            ip: "10.0.0.8",
            requestId: "req-e2e",
            createdAt: "2026-09-01T08:00:00Z",
          },
        ],
        total: 1,
        limit: 200,
        offset: 0,
      });

    if (request.method() !== "GET") return json({ ok: true });
    return json({});
  });
}
