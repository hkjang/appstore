import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { AuthProvider } from "../app/providers";
import { LoginPage } from "./auth-pages";

const REFUSAL_NOTICE = "자동으로 로그인하지 않았습니다";

function json(payload: unknown) {
  return new Response(JSON.stringify(payload), {
    status: 200,
    headers: { "content-type": "application/json" },
  });
}

// Silent SSO is on, so the callback may send the browser back here with
// ?sso=none when the identity provider has no session for it.
function renderLogin(
  url: string,
  config: Record<string, unknown> = {},
  bootstrapAvailable = false,
) {
  const fetchMock = vi.fn().mockImplementation((input: RequestInfo | URL) => {
    const target = String(input);
    if (target.includes("/auth/session")) {
      return Promise.resolve(
        json({ authenticated: false, bootstrapAvailable }),
      );
    }
    if (target.includes("/public/config")) {
      return Promise.resolve(
        json({
          siteName: "AppStore",
          oidcEnabled: true,
          oidcConfigured: true,
          oidcAutoLogin: true,
          ...config,
        }),
      );
    }
    return Promise.resolve(json({ version: "test" }));
  });
  vi.stubGlobal("fetch", fetchMock);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[url]}>
        <AuthProvider>
          <LoginPage />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return client;
}

async function renderSettledLogin(
  config: Record<string, unknown> = {},
  bootstrapAvailable = false,
) {
  const client = renderLogin("/login", config, bootstrapAvailable);
  // Both responses determine the login methods. Do not assert against the
  // temporary bootstrap form shown before the public config has arrived.
  await waitFor(() => expect(client.isFetching()).toBe(0));
}

async function findSsoLink() {
  return screen.findByRole("link", { name: /회사 계정으로 SSO 로그인/ });
}

describe("Login methods", () => {
  it("offers only SSO when no bootstrap account is available", async () => {
    await renderSettledLogin();

    expect(await findSsoLink()).toBeVisible();
    expect(screen.queryByLabelText("Bootstrap 관리자")).toBeNull();
    expect(
      screen.queryByRole("button", { name: "관리자 계정으로 로그인" }),
    ).toBeNull();
    expect(screen.queryByRole("button", { name: "관리자 로그인" })).toBeNull();
  });

  it.each([
    { oidcEnabled: false, oidcConfigured: false },
    { oidcEnabled: false, oidcConfigured: true },
    { oidcEnabled: true, oidcConfigured: false },
  ])("opens bootstrap directly when SSO is unavailable: %j", async (config) => {
    await renderSettledLogin(config, true);

    expect(await screen.findByLabelText("Bootstrap 관리자")).toBeVisible();
    expect(screen.getByLabelText("비밀번호")).toBeVisible();
    expect(screen.getByRole("button", { name: "관리자 로그인" })).toBeVisible();
    expect(
      screen.queryByRole("link", { name: /회사 계정으로 SSO 로그인/ }),
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: "관리자 계정으로 로그인" }),
    ).toBeNull();
  });

  it("keeps bootstrap behind a reversible recovery toggle when SSO is available", async () => {
    const user = userEvent.setup();
    await renderSettledLogin({}, true);

    expect(await findSsoLink()).toBeVisible();
    const toggle = await screen.findByRole("button", {
      name: "관리자 계정으로 로그인",
    });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByLabelText("Bootstrap 관리자")).toBeNull();

    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    const username = screen.getByLabelText("Bootstrap 관리자");
    expect(username).toBeVisible();
    expect(username.closest("form")).toHaveAttribute(
      "id",
      toggle.getAttribute("aria-controls"),
    );
    expect(screen.getByLabelText("비밀번호")).toBeVisible();
    expect(screen.getByRole("button", { name: "관리자 로그인" })).toBeVisible();

    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByLabelText("Bootstrap 관리자")).toBeNull();
    expect(
      screen.getByRole("link", { name: /회사 계정으로 SSO 로그인/ }),
    ).toBeVisible();
  });
});

describe("Login page after a refused silent SSO", () => {
  it("explains why the visitor was not signed in automatically", async () => {
    renderLogin("/login?sso=none");

    await findSsoLink();
    const notice = await screen.findByRole("status");
    expect(notice).toHaveTextContent(REFUSAL_NOTICE);
  });

  it("stays quiet when the address carries no sso=none marker", async () => {
    renderLogin("/login?returnTo=%2Fsubmit");

    await findSsoLink();
    expect(screen.queryByRole("status")).toBeNull();
    expect(screen.queryByText(new RegExp(REFUSAL_NOTICE))).toBeNull();
  });

  it("keeps the SSO link carrying only returnTo", async () => {
    renderLogin("/login?sso=none&returnTo=%2Fmy%2Fapps");

    const link = await findSsoLink();
    expect(link).toHaveAttribute(
      "href",
      "/api/v1/auth/oidc/login?returnTo=%2Fmy%2Fapps",
    );
    expect(await screen.findByRole("status")).toHaveTextContent(REFUSAL_NOTICE);
  });

  it("drops the notice once SSO is switched off, even from an old address", async () => {
    renderLogin("/login?sso=none", { oidcEnabled: false });

    // Without SSO there is no button the notice could point at, so the
    // screen falls back to whatever login method remains.
    expect(
      await screen.findByText(/사용 가능한 로그인 방식이 없습니다/),
    ).toBeVisible();
    expect(screen.queryByRole("status")).toBeNull();
    expect(screen.queryByText(new RegExp(REFUSAL_NOTICE))).toBeNull();
  });
});
