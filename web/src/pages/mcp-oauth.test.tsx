import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { AuthProvider } from "../app/providers";
import { AdminMcpPage, splitList } from "./admin-pages";
import { MyKeysPage } from "./personal-pages";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

const mcpSettings = {
  enabled: true,
  anonymous: false,
  rateLimitPerMinute: 60,
  protocolVersion: "2026-07-28",
  oauth: {
    enabled: true,
    resource: "",
    audience: ["claude-mcp"],
    scopes: [],
    status: {
      active: true,
      issuer: "https://sso.corp.example/realms/company",
      resource: "https://apps.corp.example/mcp",
      metadataUrl:
        "https://apps.corp.example/.well-known/oauth-protected-resource/mcp",
    },
  },
};

function renderAdminMcp(settings: unknown) {
  const calls: { url: string; method: string; body?: string }[] = [];
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        const method = init?.method ?? "GET";
        calls.push({ url, method, body: init?.body as string | undefined });
        if (url.endsWith("/admin/mcp")) return Promise.resolve(json(settings));
        return Promise.resolve(json({}));
      }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/admin/mcp"]}>
        <AdminMcpPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return calls;
}

describe("MCP SSO (OAuth) settings", () => {
  it("splits a pasted client list on spaces, commas and newlines", () => {
    expect(splitList(" claude-mcp, cursor-mcp\nvscode ")).toEqual([
      "claude-mcp",
      "cursor-mcp",
      "vscode",
    ]);
    expect(splitList("")).toEqual([]);
  });

  it("shows the live status with the addresses a client needs", async () => {
    renderAdminMcp(mcpSettings);
    expect(
      await screen.findByText(
        "https://apps.corp.example/.well-known/oauth-protected-resource/mcp",
      ),
    ).toBeVisible();
    expect(screen.getByText("https://apps.corp.example/mcp")).toBeVisible();
    expect(
      screen.getByText("https://sso.corp.example/realms/company"),
    ).toBeVisible();
    expect(screen.getByLabelText("허용 대상 (Client ID)")).toHaveValue(
      "claude-mcp",
    );
    expect(screen.getByLabelText("SSO 토큰 허용")).toBeChecked();
  });

  it("says why an enabled switch is not live, and saves the nested oauth object", async () => {
    const calls = renderAdminMcp({
      ...mcpSettings,
      oauth: {
        ...mcpSettings.oauth,
        status: {
          active: false,
          reason: "인증·SSO의 Issuer URL이 비어 있습니다",
        },
      },
    });
    const user = userEvent.setup();
    expect(
      await screen.findByText(/인증·SSO의 Issuer URL이 비어 있습니다/),
    ).toBeVisible();

    const audience = screen.getByLabelText("허용 대상 (Client ID)");
    await user.clear(audience);
    await user.type(audience, "claude-mcp cursor-mcp ");
    const scopes = screen.getByLabelText("범위 (키 권한)");
    await user.type(scopes, "mcp:read");
    await user.click(screen.getByRole("button", { name: "설정 저장" }));

    await waitFor(() => {
      const put = calls.find(
        (call) => call.method === "PUT" && call.url.endsWith("/admin/mcp"),
      );
      expect(put).toBeDefined();
      const body = JSON.parse(put!.body ?? "{}") as {
        oauth: { enabled: boolean; audience: string[]; scopes: string[] };
      };
      expect(body.oauth.enabled).toBe(true);
      expect(body.oauth.audience).toEqual(["claude-mcp", "cursor-mcp"]);
      expect(body.oauth.scopes).toEqual(["mcp:read"]);
    });
  });
});

function renderKeys(publicConfig: Record<string, unknown>) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/public/config")) {
        return Promise.resolve(
          json({ siteName: "AppStore", publicMode: true, ...publicConfig }),
        );
      }
      if (url.endsWith("/me/keys")) return Promise.resolve(json([]));
      if (url.endsWith("/me/key-permissions")) {
        return Promise.resolve(json({ permissions: [], templates: [] }));
      }
      if (url.endsWith("/auth/session")) {
        return Promise.resolve(json({ authenticated: false }));
      }
      return Promise.resolve(json({}));
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/my/keys"]}>
        <AuthProvider>
          <MyKeysPage />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("Connecting MCP with SSO instead of a key", () => {
  it("offers the MCP address when the server accepts SSO tokens", async () => {
    renderKeys({ mcpOauthResource: "https://apps.corp.example/mcp" });
    expect(await screen.findByTestId("sso-mcp-notice")).toBeVisible();
    expect(screen.getByText("https://apps.corp.example/mcp")).toBeVisible();
  });

  it("says nothing about SSO when tokens are not accepted", async () => {
    renderKeys({});
    expect(await screen.findByText("발급된 키가 없습니다")).toBeVisible();
    expect(screen.queryByTestId("sso-mcp-notice")).toBeNull();
  });
});
